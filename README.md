# Beethoven

Background coding agents you run yourself. Give it a repository and a prompt — from the web UI, the
CLI, Slack, a cron schedule or a webhook — and an agent works on it in an isolated container while you do
something else. Everything it does streams live and is kept as a replayable log; follow-up prompts
queue on the same session and continue the same conversation; when the work is ready the agent opens
a pull request authored by you. Agents can split work across parallel child sessions.

One Go binary, SQLite, Docker and Claude Code, with a small Preact UI built in.

## Quick start

Requires Go 1.26+, Bun and Docker.

```sh
make                     # builds the web UI and bin/beethoven
bin/beethoven serve      # first start builds the sandbox image (a few minutes)
```

Open http://127.0.0.1:7070 and click **Create the GitHub App**: GitHub opens with Beethoven's app filled
in, you confirm it, pick the repositories agents may work on, and you're back (see [GitHub](#github)).
Then add an Anthropic API key or a Claude subscription token (from `claude setup-token`) under
**Settings › Credentials**. Or use the CLI:

```sh
bin/beethoven run -repo you/project "Fix the flaky test in auth_test.go and open a PR"
bin/beethoven run -env "web stack" "Rename the user endpoint in the API and the frontend"
bin/beethoven ls
bin/beethoven send <session> "Also add a regression test"
bin/beethoven secret import -scope env:"web stack" < .env
```

## Self-hosting

Beethoven is one server plus a sandbox per session. The server can run anywhere; sandboxes run on
the server's own Docker or on [Modal](https://modal.com), which suits hosts without Docker such as
Railway. Everything lives in one data directory: back it up, `secret.key` included, since it
encrypts the stored secrets.

### On Railway, with sandboxes on Modal

Railway runs the server, Modal runs the sandboxes. You need a Railway account and a Modal account
(Modal's free monthly credit covers light use). A coding agent can run every step; signing in and
creating the Modal token need you.

1. Create a Modal token: Modal's dashboard, **Settings › API Tokens**, or `modal token new`.
2. From a clone of this repository:

   ```sh
   railway init --name beethoven
   railway add --service beethoven
   railway service link beethoven
   railway variable set --skip-deploys MODAL_TOKEN_ID=<id> MODAL_TOKEN_SECRET=<secret>
   railway volume add --mount-path /data   # keeps the database across deploys
   railway domain                          # a public HTTPS address
   railway up --detach                     # builds the Dockerfile and deploys it
   ```

3. Open the domain as soon as the deploy is up and create your account: on a new server, the first
   visitor creates the account that owns it. Then connect GitHub and Slack and add an Anthropic key
   in **Settings**.

Railway assigns the port and the domain; Beethoven listens on that port and uses the domain as its
address for GitHub, Slack and its sandboxes. Optionally, set the service's healthcheck path to
`/healthz` (**Settings › Deploy** in Railway) so deploys wait until the server is up. The first
session waits a few minutes while Modal builds the sandbox image; Beethoven starts building it as soon
as the server starts. Until Modal is configured the server runs, and sessions explain what's missing.

### On a machine with Docker

Use the [quick start](#quick-start) on a Mac or Linux machine with Docker, or run the server's image
with the Docker socket mounted. To reach it from the internet, put it behind HTTPS (for testing, a
tunnel such as `cloudflared tunnel --url http://localhost:7070` works) and set `BEETHOVEN_URL` and
`BEETHOVEN_HOSTS`; the server then asks people to sign in.

## How it works

```
 web UI · CLI · cron · webhooks                      every source is a session.Request
              │  HTTP + server-sent events
┌─────────────▼──────────────────────────────────────────────┐
│ beethoven serve                                            │
│  Manager.Submit ─▶ runner (one per session)                │
│                    ├─ prompt queue    (FIFO, one at a time)│
│                    ├─ event log       (append-only, SQLite)│
│                    └─ sandbox lease   (start / idle stop)  │
│  MCP tool server ◀── agent tool calls (per-session token)  │
└─────────────┬──────────────────────────────▲───────────────┘
              │ docker exec                  │ stdout: stream-json
┌─────────────▼──────────────────────────────┴───────────────┐
│ sandbox container: /workspace/repo + claude -p             │
└────────────────────────────────────────────────────────────┘
```

**Session.** A prompt queue, an event log and a sandbox, owned by one runner. Sessions are archived,
never deleted, so their history stays available. Prompts run strictly in
order; a prompt sent while the agent works waits its turn. Stop ends the running prompt and cancels
the queue. A session's status is derived from its prompts: `running`, then `done`, `failed` or
`stopped`.

**Event log.** Everything a client sees is an event in one append-only, per-session sequence:
prompts, agent messages, tool calls and results, sandbox phases, pull requests, child results. Clients
stream `GET /api/sessions/{id}/events` and resume from `Last-Event-ID`, so reconnecting, replaying
history and following live are the same operation. Streaming text deltas are sent live but not
stored.

**Attachments.** A prompt can carry files: picked, pasted or dropped in the web UI, or shared in
Slack, up to 10 files and 50 MB in all. They're stored with the prompt and shown in its timeline. When
the prompt runs, they're copied into the sandbox, to `~/attachments/<prompt id>/`, and the agent is told
where they are, so it reads the ones it needs. Claude Code's Read tool shows it images and PDFs.

**Sandbox.** One container per session behind a four-method interface: `Start` (create or resume),
`Exec`, `Stop` (keep the filesystem), `Destroy`. A sandbox stops after 10 idle minutes and resumes on
the next prompt with its workspace, installed dependencies and agent conversation intact. Nothing of
ours runs inside it: the server drives it with `exec`, so a finished process is a finished turn and
there are no bridges, heartbeats or connection watchdogs to keep alive. Stop kills the agent's whole
process tree inside the container. Two providers implement it: **Docker** on the server's machine,
where a stop keeps the container, and **Modal**, where a stop snapshots the filesystem and the next
start creates a sandbox from the snapshot. Modal builds the same sandbox image from the Dockerfile,
and keeps snapshots for its default 30 days; a session idle longer starts over from a fresh clone.

**Agent.** A harness is two functions: build the command for a turn, and decode its output into
events. The Claude Code harness runs `claude -p --output-format stream-json` and resumes the previous
conversation with `--resume`. Adding another agent is one file.

**Tools.** The server hosts an MCP server that every agent connects to with its session's token:
`create_pull_request`, `show_file` (see Viewer), `comment` and `add_labels` (on issues and pull requests), `pull_request_status` (GitHub's mergeable state and CI checks for the
session's PR), `spawn_session`, `send_prompt`, `get_session` (which can wait for a child to
finish), `list_sessions`, `stop_session`, and `list_capabilities` and `add_capability` (see
Capabilities). Agents only reach their own direct children; children nest at most two levels deep.

**Pull requests.** The agent commits; `create_pull_request` makes the server push the session's
branch (`beethoven/<id>`) from inside the sandbox with a token passed to that one command, then open
the PR through the GitHub API as the person the session works for: whoever started it. Git
credentials are never written into the sandbox or given to the agent. Commits are authored by whoever
sent the prompt (their connected GitHub account, with its private `noreply` address, else their name
and email) and committed by `Beethoven`.

**People and teams.** A server that asks people to sign in has an owner, who invites the others with
single-use links (valid for 7 days, sent however you like) and gives each a role: **owner**, **admin**
or **member**. An invitation can add its person to a team, carry a note and suggest a first session;
its page explains what Beethoven is, and someone who already has an account can use it to join the
team. Whoever joins gets a short tour and a **Get started** checklist: connect GitHub, give the team a
project if it has none yet (from existing repositories or a new one), and start a first session. A
team's first project becomes its default. Owners and admins manage people, teams, settings and integrations; only owners make or
unmake owners, and the last one can't leave. Admins create **teams**: a team has leads and members,
and owns its sessions, projects, automations, secrets and Slack channels. Leads manage the team's
people, secrets, channels and settings; members use and manage its projects and automations; anyone
may join a team that's open to all. Work is private until it's shared: everyone also has a
**personal team** that only they see (admins and owners included), which nobody can join. New
sessions, projects and automations go to the person's default team: the team their invitation added
them to, else their personal team (they can pick another under their profile). A session belongs to a
team, or to the workspace (no team, only when asked for), and is seen by **everyone**, its **team** (a
team's default) or **only** whoever started it (a person's workspace session, unless they say
otherwise); its owner or the team's lead can change that, and can share it with named people, who may
read it but not prompt it. Workspace owners see private sessions and admins team ones, except in
personal teams. Only a team's members prompt its sessions, whoever can see them. Projects and
automations without a team, made before personal teams or asked for, are everyone's; so are a
workspace automation's runs, like the automation (a team's automation's runs take the team's default
visibility). Nothing in a personal team reaches admins, even once its person is deactivated: before
someone leaves, have them share or move what should outlive them; afterwards, only the server's own
token (`BEETHOVEN_TOKEN`) reaches it. Teams are archived, never
deleted: an archived team takes no new work. Changes to who may do what (people, teams, memberships,
secrets, channels, session visibility) go to an audit log that admins, and a team's leads for their
team, can read; a personal team's events are its person's alone. The sidebar's switcher shows one team's sessions, the workspace's, or all you can
see, grouped by project; new sessions start in the chosen team. **⌘K** (Ctrl+K) searches every
session, project and page you can open, and **C** starts a new session.

**Automations.** A saved request: a prompt, a repository, and a cron schedule, a repository event
and/or a webhook. Firing one submits a prompt like any other source; that prompt is the run.
Scheduled and webhook runs don't overlap, webhook redeliveries (same `Idempotency-Key` or
`X-GitHub-Delivery`) fire once, the webhook body is handed to the agent, and three failures in a row
pause the automation. With "reuse", every run continues one session.

**Repository events.** An automation can fire when something happens in the repositories it
targets: an issue opened, labeled, closed or commented on; a pull request opened, updated, ready for
review, labeled, merged, closed, reviewed or commented on; CI finishing; a push. Conditions narrow it
down, all of which must hold: the label just added, labels it has (or lacks), branch and target
branch patterns, who did it, draft, text in the comment or description, CI conclusion and workflow
name. So "issue labeled `agent`" fixes issues, "pull request opened, not a draft, into `main`"
reviews them, and "CI finished with `failure` on `main`" investigates. Each run gets the event with
the prompt, fenced as data the agent mustn't take instructions from, and starts from the pull
request's branch when there is one; with "reuse", events about the same issue or pull request
queue on one session. Bots' comments never trigger runs, so agents can't set each other off. The
agent answers with the `comment` and `add_labels` tools. Events are host-neutral (`internal/event`):
the GitHub integration verifies deliveries to `POST /webhooks/github` and translates them, and
another git host would only need its own translation. A GitHub App created from this server at a
public HTTPS address subscribes to the events itself; otherwise add a repository webhook (JSON,
the events above) with `BEETHOVEN_GITHUB_WEBHOOK_SECRET` as its secret. Apps created before this
need the Issues, Checks and Actions permissions and the events turned on in their GitHub settings.

**Viewer.** A resizable panel beside a session shows files from its sandbox: mockups, screenshots,
generated images, Markdown, PDFs, CSV, code. The agent calls `show_file` when someone asks to see a
file or after it makes something visual; the server copies the file out (up to 25 MB), so it stays
viewable after the sandbox stops, and showing the same path again adds a version. Each file opens in
a tab with its versions, a download, and, for HTML, phone, tablet and full widths. What an agent
wrote is untrusted: files are served with a `Content-Security-Policy` sandbox, so HTML runs its own
scripts in an opaque origin with no access to the app's cookies or API, and nothing else runs
scripts at all.

**Projects.** A named, reusable workspace (an *environment* in the API): an ordered list of up to ten
repositories (the first is the primary, each with its own base branch), setup and start scripts, its
own secrets, and overrides of the global settings (model, effort, instructions, sandbox image, CPUs,
memory). A project belongs to a team (by default its maker's default team) or the workspace; only a team's members see and use its
projects, and a team's project starts only that team's sessions. A session started from a project
copies its repositories and checks each out at `/workspace/<name>`; editing the project later doesn't
move existing sessions, but its scripts, secrets and settings are read live. A session can also
target several repositories without a project, one, or none.

**New projects.** **New project** (in the team switcher's menu) starts something from nothing:
Beethoven creates the GitHub repository (private by default) with a first commit holding a README and
the stack's `.gitignore`, and a project named after it, working on it, in the chosen team. The stack
(Node.js, Expo, Bun, Python, Go, Rust, or anything) switches on the project's matching capability,
which installs the toolchain and then the dependencies once there are any (for Node, which the
sandbox has, a setup script installs the packages); a pasted `.env` becomes the project's secrets. Given a first task, a session starts there straight away; otherwise new sessions
default to the project. Creating repositories needs the GitHub App's Repository creation permission
(see below).

**Secrets.** Environment variables for sandboxes, encrypted with AES-256-GCM under a key in the data
directory. Every secret has a scope — global, a repository, a team, or a project — and a session gets,
lowest precedence first: global secrets, its team's, then either its project's secrets (a project is
a boundary: its repositories' own secrets don't apply, and it can opt out of global ones) or its
repositories' secrets, the primary winning. Admins manage global and repository secrets, a team's
leads its secrets, and whoever manages a project its secrets. `GET /api/secrets/effective` shows exactly what a session
would receive and what overrides what. Values are write-only; names that would shadow the server's
own variables are refused. Paste a `.env` file to import many at once.

**Settings.** Global defaults live in the database and are edited in the UI: model and effort, extra
instructions for every agent, draft pull requests, the branch prefix, the commit author, and the
sandbox image, CPUs, memory, idle timeout and prompt time limit. Environments override the agent and
sandbox ones; a prompt can override model and effort. Credentials (Anthropic, GitHub) are stored
encrypted, separate from secrets, so they reach the agent whatever an environment inherits.

**MCP servers.** Further tool servers for agents — remote over HTTP with headers, or a command run
in the sandbox with its environment — offered globally, per environment or per repository.

**Capabilities.** Toolchains a sandbox installs on demand, each with a Claude Code skill that tells
the agent how to use it: `expo` (mobile apps with Expo and React Native; checks with TypeScript,
Jest and Metro bundles, native builds and phone previews through EAS with an `EXPO_TOKEN` secret),
`android` (JDK 17 and the Android SDK, for local Gradle builds), `go`, `rust`, `bun` and `python`
(uv). Nobody has to pick them:

- The image is a lean base with Node 24 and Python 3.
- A sandbox installs the capabilities its repositories look like they need (`go.mod` brings Go,
  `android/build.gradle` the Android SDK, an Expo dependency Expo), after cloning and before the
  setup scripts. An `android/` directory means a download of several GB on the first boot, until the
  install is kept. Beethoven remembers what each repository needed at its last boot, on whatever
  branch, so its next sandbox is created with the CPUs and memory they want (Android builds want 4
  CPUs and 8 GB).
- Installs are kept in `<data>/cache/capabilities`, by capability version, machine and image, and
  unpacked into later sandboxes instead of being downloaded again. They expire after a week, so
  toolchains that install their latest release are refreshed, and each is at most 4 GB. Only a
  brand-new sandbox packs one, before any repository code runs and without the session's secrets,
  so nothing an agent, a repository or a secret could touch reaches other sessions.
- The agent adds others mid-session with the `add_capability` tool when a task needs them. They are
  kept for later in the session's environment, else in its primary repository's
  `.beethoven/capabilities` (for the agent to commit).

The global settings, an environment or a repository's `.beethoven/capabilities` file can also name
capabilities, for needs a repository's files don't show yet. A failed install is a warning.

**Plugins.** [Claude Code plugins](https://docs.claude.com/en/docs/claude-code/plugins) for every
agent, with their skills, commands and subagents: the global settings name them, and a project adds
its own. Each is `plugin@marketplace`, the marketplace as `owner/repo`, a git URL or an https URL
(`compound-engineering@EveryInc/compound-engineering-plugin`). A sandbox installs the latest version
of each once, after the capabilities and before the setup scripts, with `claude plugin install
--marketplace`, which needs a recent Claude Code in the image; a failure is a warning, retried on the
next start. Marketplaces are cloned without the session's GitHub credentials, so private ones need
their own access.

### GitHub

Beethoven works on GitHub as a GitHub App, which a person connects by authorizing it. The app is
either:

- **Preconfigured**: one app an operator registered once, shared by every server they run (a hosted
  Beethoven). Give its credentials in `BEETHOVEN_GITHUB_APP`; people only connect and install it.
- **Created from the server**: without a preconfigured app, the UI registers one for you with
  GitHub's [app manifest flow](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest).
  It is owned by your account or an organization and stored encrypted in the database. Make it public
  if it should be installable on several accounts or organizations, and to create the shared app
  for a preconfigured setup.

Either way the app asks for contents, pull requests and workflows (write), repository creation
(write, for new projects; the app gets access to the repositories it creates and nothing else) and
metadata (read), and connecting takes the browser to GitHub and back: create (only without a
preconfigured app; an admin does this), authorize, install. **Everyone connects their own GitHub
account**, and a session works as the person who started it: the server clones and pushes with
installation tokens it creates for one repository at a time, valid for an hour, and only through
installations that person can access; public repositories the app isn't installed on are cloned
anonymously. Pull requests, branch and repository lists use the person's own token, which refreshes
itself. Someone who hasn't connected GitHub can't work on repositories; nobody borrows another
person's access. Work no person asked for (the server's own token, a server nobody signs in to) acts
as the owner.

`BEETHOVEN_GITHUB_APP` holds the app's credentials as JSON, the way GitHub's manifest conversion
returns them (`id`, `slug`, `client_id`, `client_secret`, `pem`, optional `name`, `owner.login`), or
the path of a file containing it. Its callback URL must be the server's URL plus `/github/authorized`,
and its setup URL the server's URL plus `/github/installed`.

An app registered before Beethoven asked for repository creation can't start projects until its
owner sets **Repository creation** to *Read and write* in the app's permissions and each account
accepts the change on its installation; **New project** links to both.

Until anyone connects, a personal access token is used instead, for everyone: one saved under
**Settings › GitHub**, `GITHUB_TOKEN`, or the GitHub CLI's (`gh auth token`).

### Slack

Mention **@beethoven** in a channel, or send it a direct message, and it works on the request in a
session and answers in the thread: 👀 on the request, a status reply with **Open in Beethoven** and
**Stop**, then the agent's answer and its pull requests, and ✅ or ❌. The thread is the session:
mention the bot in it again (any reply, in a direct message) to queue a follow-up, which carries what
was said in the thread since as context. Files shared with the request, or in the thread since the bot
was last asked there, are attached to its prompt. A request works on the repositories (`owner/repo`) or the
project it names, else on the channel's default; otherwise the bot asks, and the answer can become
the channel's default. Full members of the workspace can start sessions; guests and people from
other organizations in shared channels can't. A requester is matched to a person here by the email on
their Slack profile, and the session works as them: their GitHub, their commits. A channel can belong
to a team (**Settings › Slack**, or the team's page): its sessions are the team's, and only its
members may ask there; a direct message uses the person's default team. Someone without an account
here works as the server's owner, unless **Settings › Slack** allows only people with accounts.

Like GitHub, Slack is reached through an app, which is either:

- **Preconfigured**: an operator's app, given in `BEETHOVEN_SLACK_APP` as JSON (`app_id`,
  `client_id`, `client_secret`, `signing_secret`) or a file holding it. People click **Add to Slack**.
  Its event and interactivity URLs are the server's URL plus `/slack/events` and
  `/slack/interactions`, and its redirect URL the server's URL plus `/slack/installed`.
- **Created from the server**: paste an [app configuration token](https://api.slack.com/apps) (under
  *Your App Configuration Tokens*) in **Settings › Slack**, and the server creates its app with
  Slack's manifest API, then sends you on to add it to the workspace. The token is used once and not
  stored.

The bot needs the `files:read` scope to read shared files. Apps created before Beethoven handled files
lack it. Add it under **OAuth & Permissions** in the app's settings on api.slack.com, then reinstall
the app. Until you do, the bot replies with these steps whenever someone shares a file.

Slack only delivers events to a public HTTPS address, so the server needs one; for local testing, a
tunnel such as `cloudflared tunnel --url http://localhost:7070` works, with its host in
`BEETHOVEN_HOSTS`.

### Threads: agents hearing back

Sessions take part in threads on GitHub, and what's said there comes back to them as a new prompt,
so agents answer reviewers, CI and each other without anyone copying messages across. A session
**owns** the pull request (and branch) it opens, and **follows** the issues and pull requests it
comments on, the one an automation started it for, and any it's asked to with the `follow` tool.
When an automation's agent reviews a pull request another session opened, its review reaches that
session, which makes the changes, pushes and replies; the reply reaches the reviewer in turn.

- **What's passed on**: comments, reviews (not bare approvals) and line comments, and failing CI on
  a session's own branch. A merged or closed pull request ends the thread; reopening it takes the
  thread up again.
- **Batched**: what arrives together is handed over as one prompt once the thread has been quiet for
  ten seconds (at most a minute), so a review with many line comments is one prompt. The inbox is
  in the database: nothing is lost to a restart, and redeliveries are ignored.
- **Bounded**: what a session posts is recorded, so its own words never come back to it. Only that
  record makes a comment count as an agent's; the hidden marker comments carry is a hint to wait
  for the record, since anyone can type one or copy one when quoting. Each prompt counts the
  exchanges between agents that led to it; past four, it's noted on the timeline for a person
  instead of run. Failing CI counts as one more round after the prompt that pushed. A session is
  handed at most twelve prompts an hour. An automation never fires on its own runs' comments, and
  its runs aren't told a second time what it ran on itself (judged by what it did, once it has).
  A merged or closed pull request stays ended until it's reopened.
- **Trusted**: only people who can write to the repository (owners, members, collaborators) and
  this server's agents are heard; other apps' comments and strangers' on public repositories aren't.

The session page lists what it follows, with mute and leave, and what came from its threads,
including anything held back and why.

### Insights

The **Insights** page measures how sessions go over the last 7, 30 or 90 days, compared with the
days before, in tabs. Filters above the tabs apply to all of them and live in the URL, so a link
opens the same view: the window, a team, a repository, and a **scope**. The scope picks the
sessions measured: the ones people started (the default), the ones automations started, the ones
agents spawned, or all. Work outside the scope (spend, prompts, tokens, pull requests) counts for
the session in scope above it, so a person's session pays for the helpers its agent spawned.

- **Overview**: quality first, then throughput, each with its change and a daily trend. Quality
  counts sessions once they settle, meaning they ran and have nothing queued or running. A
  **cycle** is one prompt and its response; a session's cycles are its prompts that ran. The
  **first-try rate** is the settled sessions that succeeded in one cycle; a session succeeds when
  its last cycle finished and none of its pull requests was closed without merging. Then outcomes
  per day, cycles to finish, and the top repositories, models and people.
- **Usage**: sessions per day by where they started (web, CLI, Slack, automations, agents), how
  they ended, when in the week work starts, and breakdowns by repository, project, team, and model
  and effort.
- **Cost**: spend, per session and per merged pull request, by model and by team, the most
  expensive runs, and tokens with the cache hit rate. Spend and tokens are what the agent reports;
  tokens and the model that ran are recorded per prompt.
- **Pull requests**, from every source whatever the scope: opened, acceptance, the time to merge
  and its spread, where they stand, by source and repository, and those open now, oldest first.
- **People**: active people per week, how far the people invited in the last 90 days have got
  (joined, a first session, a first merged pull request), and everyone's sessions, quality, pull
  requests and spend, with a sheet per person.
- **Reliability**, for admins: prompt failures by reason, prompt duration, queue wait and sandbox
  start time, tool calls that returned an error, and how each automation's runs went.

Viewers only count the sessions they may read.

Each session's header shows its cycles, the agents' working time and its pull requests' state, and
each automation shows how its recent runs went. Pull requests are tracked by polling GitHub every
ten minutes until they are merged or closed. Sessions are archived, never deleted, so the numbers
keep their history.

### Repository hooks

- `.beethoven/capabilities` names capabilities to install besides those the repository looks like
  it needs, one per line (`#` starts a comment), before the scripts below run.
- `.beethoven/setup.sh` runs once per sandbox after the clone (dependencies, builds). Failure is a
  warning.
- `.beethoven/start.sh` runs every time the sandbox starts (services). Failure fails the prompt.

## Configuration

| Variable | Default | |
|---|---|---|
| `BEETHOVEN_ADDR` | `127.0.0.1:7070`, or `0.0.0.0:$PORT` when `PORT` is set | listen address |
| `BEETHOVEN_URL` | Railway's domain, if any | the server's public HTTPS address |
| `BEETHOVEN_DATA` | `~/.beethoven` | database and encryption key |
| `BEETHOVEN_TOKEN` | — | an API token for scripts and the CLI (`Authorization: Bearer`), accepted instead of signing in; it acts as the server itself, for its owner. People make their own under **Settings › Account** |
| `BEETHOVEN_HOSTS` | — | extra `Host` names to accept (for access through a proxy) |
| `BEETHOVEN_SANDBOX` | `modal` when `MODAL_TOKEN_ID` is set, else `docker` | sandbox provider |
| `MODAL_TOKEN_ID`, `MODAL_TOKEN_SECRET` | `~/.modal.toml` | Modal credentials |
| `BEETHOVEN_MODAL_APP` | `beethoven` | the Modal app sandboxes belong to |
| `BEETHOVEN_DOCKER_NETWORK` | — | a Docker network for sandboxes, e.g. one shared with the server |
| `BEETHOVEN_IMAGE` | `beethoven-sandbox:latest` | sandbox image (built from the Dockerfile; any other name is pulled) |
| `BEETHOVEN_SANDBOX_URL` | Docker: `http://host.docker.internal:<port>`; Modal: `BEETHOVEN_URL` | the server as seen from sandboxes |
| `BEETHOVEN_IDLE` | `10m` | stop idle sandboxes after |
| `BEETHOVEN_TURN_TIMEOUT` | `2h` | limit for one prompt |
| `BEETHOVEN_MODEL` | — | initial default model; then edit it in Settings |
| `BEETHOVEN_GITHUB_APP` | — | a preconfigured GitHub App: its credentials as JSON, or a file (see [GitHub](#github)) |
| `BEETHOVEN_SLACK_APP` | — | a preconfigured Slack app: its credentials as JSON, or a file (see [Slack](#slack)) |
| `GITHUB_TOKEN` | `gh auth token` | personal access token used until someone connects GitHub, unless one is saved in Settings |
| `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN` | — | passed to sandboxes unless saved in Settings |

Settings saved in the UI take precedence over `BEETHOVEN_IDLE`, `BEETHOVEN_TURN_TIMEOUT` and
`BEETHOVEN_IMAGE`, which act as defaults.

On Linux, sandboxes reach the server through the Docker bridge, so listen on an address they can
reach (for example `BEETHOVEN_ADDR=0.0.0.0:7070`); the UI then asks you to sign in.

## Security model

Trusted repositories. The API rejects cross-site requests and foreign `Host` headers (CSRF and DNS
rebinding). On localhost it is open to this machine; a server reachable beyond it (listening on
another address, or accepting other host names) asks people to sign in with an email and password.
Passwords are stored as salted PBKDF2-SHA256 hashes; a signed-in browser holds an `HttpOnly`,
`SameSite=Lax` cookie for 30 days, stored only as its hash; ten failed sign-ins for an email within
15 minutes block it until the window passes. A new server belongs to whoever creates the first
account, so create it as soon as the server is up. Others join only through an invitation link,
stored as its hash, used once, and valid for 7 days; a deactivated account is signed out everywhere
and its API tokens stop working. Personal API tokens act as their person and are stored as hashes;
`BEETHOVEN_TOKEN`, when set, acts as the server itself. Who may read, prompt or manage a session, use
a project, or change a secret is decided by one set of rules (`internal/access`), mirrored by the
query that lists sessions, with a test that the two agree; something you may not see answers 404, and
live lists only carry sessions you may see. Agents run
with permissions bypassed inside their container, see the sandbox secrets, and can reach the network.
Their tool token only acts on their own session and its children. GitHub tokens reach a sandbox only
for the clone and push commands the server runs, each limited to one repository for an hour. GitHub
returns the browser to `/github/<step>`, outside the signed-in API, so those pages only redirect to the UI,
which completes each step through the API; a single-use state ties each return to the request that
started it; Slack's installation works the same way. Requests from Slack must carry a valid signature
from the last five minutes, a redelivered event is acted on once, the bot ignores bots (itself
included), and what was said in a thread reaches the agent marked as context, not instructions. The
bot downloads shared files with its token from Slack's own hosts only. Attached files are served with
`nosniff` and a sandboxing CSP, and only images and plain text open in the browser; everything else,
HTML and SVG included, downloads, so a file can't run script as the person viewing it.

## API

| | |
|---|---|
| `GET/POST /api/sessions` | list (`?team=` ids or `workspace`, `?mine=1`, `?q=` title, `?archived=1` or `all`); create `{prompt, environment_id \| repos, model, effort, title, team_id, visibility}`; `team_id` defaults to the project's team, else the person's default team; `"workspace"` asks for none |
| `GET /api/sessions/{id}` | session, prompts and pull requests |
| `POST /api/sessions/{id}/prompts` | queue `{content, model}` |
| | to attach files to either, send a multipart form: the JSON in a `request` field, each file in a `file` field |
| `GET /api/sessions/{id}/attachments/{aid}` | an attached file |
| `DELETE /api/sessions/{id}/prompts/{pid}` | cancel a prompt |
| `POST /api/sessions/{id}/stop` · `/archive` · `/pr` | stop; `{archived}`; `{repo, title, body, draft}` |
| `POST /api/sessions/{id}/capabilities` | install a capability `{id}` now and keep it for the session's environment or repository (the agent's `add_capability`) |
| `GET /api/capabilities` | the capabilities sandboxes can be given; settings and environments list theirs in `capabilities` |
| `GET /api/auth` · `POST /api/auth/setup` · `/login` · `/logout` | who is signed in; create the first account; sign in and out |
| `GET /api/sessions/{id}/events` | server-sent events; `Last-Event-ID` resumes |
| `GET /api/sessions/{id}/threads` · `PUT` · `DELETE ?subject=` | the threads it takes part in and what came from them; mute one; leave one |
| `GET /api/events` | live state of every session |
| `/api/automations[/{id}[/run\|/runs]]` | CRUD, run now, run history |
| `GET /api/sessions/{id}/artifacts[/{aid}]` | files shown in the viewer; one file's content (`?download=1` saves it) |
| `POST /hooks/{id}` | fire an automation; `Authorization: Bearer <hook token>` |
| `POST /api/automations/{id}/hook-token` | make a new webhook token for an automation, replacing the old one; shown once |
| `POST /webhooks/github` | GitHub event deliveries, signed with the app's webhook secret |
| `GET /api/automations/events` | the event kinds and conditions automations can trigger on |
| `GET/PUT/DELETE /api/secrets` | by scope; names only on read |
| `POST /api/secrets/import` · `GET /api/secrets/effective` | `.env` import; what a session receives |
| `/api/environments[/{id}]` | projects: CRUD; `team_id` on creation |
| `GET /api/teams` · `POST /api/teams` · `GET/PUT /api/teams/{id}` | teams (by id or short name) |
| `POST /api/teams/{id}/archive` · `/join` | `{archived}`; join an open team |
| `PUT/DELETE /api/teams/{id}/members/{account}` | `{role: lead \| member}`; remove, or leave |
| `GET /api/people` · `PUT /api/people/{id}` | everyone; `{role, disabled}` |
| `GET/POST /api/invites` · `DELETE /api/invites/{id}` | invitation links |
| `GET/POST /api/auth/invite` | an invitation's details, or whether it was used or expired; accept it `{token, name, email, password}` |
| `POST /api/auth/invite/join` | use an invitation while signed in: join its team `{token}` |
| `PUT /api/me` · `GET/POST /api/tokens` · `DELETE /api/tokens/{id}` | `{name, default_team_id}`; personal API tokens |
| `PUT /api/me/onboarding` | getting started: `{tour_seen, started, done}` |
| `PUT /api/sessions/{id}/visibility` | `{visibility: workspace \| team \| private}` |
| `PUT /api/sessions/{id}/shares` | `{account_ids}`: the people it's shared with, who may read it |
| `PUT /api/slack/channels/{team}/{channel}/team` | `{owner_team_id}`: the team a channel belongs to |
| `GET /api/audit?team=&before=` | changes to who may do what |
| `GET /api/projects/options` | where a new project's repository can go, and the stacks |
| `POST /api/projects` | start a project `{owner, name, description, public, stack, variables, prompt, team_id}`; files as for sessions |
| `GET/PUT /api/settings` · `/api/credentials` | global settings; write-only credentials |
| `GET /api/github` | the GitHub App, the connected account and its installations |
| `POST /api/github/app` · `/app/created` · `DELETE /api/github/app` | register an app (manifest flow); forget it |
| `POST /api/github/authorize` · `/account` · `DELETE /api/github/account` | connect an account; disconnect it |
| `GET /api/slack` | the Slack app, its workspaces and channel defaults |
| `POST /api/slack/app` · `/install` · `/installed` | create the app; add it to a workspace |
| `DELETE /api/slack/app` · `/installations/{team}` · `/channels/{team}/{channel}` | remove them |
| `POST /slack/events` · `/slack/interactions` | Slack's events and clicks (signed by Slack) |
| `/api/mcp-servers[/{id}]` | CRUD; header and env values write-only |
| `GET /api/insights?days=&tz=&scope=&team=&repo=` | quality, usage, cost, pull requests and people of the last days, and the days before |
| `GET /api/insights/reliability?…` | failures, prompt and sandbox times, tool errors and automations (admins) |
| `GET /api/repos[/{owner}/{name}/branches]` | GitHub repositories and branches, for pickers |
| `/mcp` | agent tools (session tokens) |

## Development

```sh
make test-short       # unit tests, no Docker
make test             # plus the Docker end-to-end test
make fake-image       # a sandbox image whose `claude` is a script: no API calls
BEETHOVEN_IMAGE=beethoven-fake:latest bin/beethoven serve
BEETHOVEN_TEST_MODAL=1 go test -run TestModalLive ./internal/sandbox/   # real Modal sandboxes; costs cents
```

The fake agent understands `COMMIT`, `SLEEP n` and `FAIL` in prompts.

```
cmd/beethoven        server and CLI
internal/session     sessions: queue, event log, sandbox lifecycle, PRs, boot script
internal/sandbox     Provider interface, Docker and Modal implementations, sandbox Dockerfile
internal/agent       Harness interface, Claude Code implementation
internal/tools       MCP tool server
internal/automation  schedules, webhooks, repository events, failure policy
internal/relay       threads' comments, reviews and CI back to their sessions, within bounds
internal/event       host-neutral repository events and the triggers that match them
internal/store       SQLite migrations and queries, environments, settings, encrypted secrets
internal/github      the GitHub App, its tokens and accounts, clone URLs, pull requests
internal/slack       the Slack app, its installations, and the bot
internal/insights    cycles, first-try rate, usage, cost, pull requests, people and reliability
internal/api         HTTP API, event streams, guards
web                  Preact UI, embedded in the binary
```

## License

[MIT](LICENSE). Beethoven was inspired by Ramp's [Inspect](https://builders.ramp.com/post/why-we-built-our-background-agent)
and [Open-Inspect](https://github.com/ColeMurray/background-agents).
