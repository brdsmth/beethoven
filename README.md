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
`create_pull_request`, `spawn_session`, `send_prompt`, `get_session` (which can wait for a child to
finish), `list_sessions` and `stop_session`. Agents only reach their own direct children; children
nest at most two levels deep.

**Pull requests.** The agent commits; `create_pull_request` makes the server push the session's
branch (`beethoven/<id>`) from inside the sandbox with a token passed to that one command, then open
the PR through the GitHub API as the person the session works for: whoever started it. Git
credentials are never written into the sandbox or given to the agent. Commits are authored by whoever
sent the prompt (their connected GitHub account, with its private `noreply` address, else their name
and email) and committed by `Beethoven`.

**People and teams.** A server that asks people to sign in has an owner, who invites the others with
single-use links (valid for 7 days, sent however you like) and gives each a role: **owner**, **admin**
or **member**. Owners and admins manage people, teams, settings and integrations; only owners make or
unmake owners, and the last one can't leave. Admins create **teams**: a team has leads and members,
and owns its sessions, projects, automations, secrets and Slack channels. Leads manage the team's
people, secrets, channels and settings; members use and manage its projects and automations; anyone
may join a team that's open to all. A session belongs to a team, or to the workspace (no team), and
is seen by **everyone**, its **team** (a team's default) or **only** whoever started it; its owner or
the team's lead can change that. Only a team's members prompt its sessions, whoever can see them.
Work without a team belongs to the workspace and behaves as before teams. Teams are archived, never
deleted: an archived team takes no new work. Changes to who may do what (people, teams, memberships,
secrets, channels, session visibility) go to an audit log that admins, and a team's leads for their
team, can read. The sidebar's switcher shows one team's sessions, the workspace's, or all you can
see, grouped by project; new sessions start in the chosen team.

**Automations.** A saved request: a prompt, a repository, and a cron schedule and/or a webhook.
Firing one submits a prompt like any other source; that prompt is the run. Runs don't overlap,
webhook redeliveries (same `Idempotency-Key` or `X-GitHub-Delivery`) fire once, the webhook body is
handed to the agent, and three failures in a row pause the automation. With "reuse", every run
continues one session.

**Projects.** A named, reusable workspace (an *environment* in the API): an ordered list of up to ten
repositories (the first is the primary, each with its own base branch), setup and start scripts, its
own secrets, and overrides of the global settings (model, effort, instructions, sandbox image, CPUs,
memory). A project belongs to a team or the workspace; only a team's members see and use its
projects, and a team's project starts only that team's sessions. A session started from a project
copies its repositories and checks each out at `/workspace/<name>`; editing the project later doesn't
move existing sessions, but its scripts, secrets and settings are read live. A session can also
target several repositories without a project, one, or none.

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

Either way the app asks for contents, pull requests and workflows (write) and metadata (read), and
connecting takes the browser to GitHub and back: create (only without a preconfigured app; an admin
does this), authorize, install. **Everyone connects their own GitHub account**, and a session works as
the person who started it: the server clones and pushes with installation tokens it creates for one
repository at a time, valid for an hour, and only through installations that person can access;
public repositories the app isn't installed on are cloned anonymously. Pull requests, branch and
repository lists use the person's own token, which refreshes itself. Someone who hasn't connected
GitHub can't work on repositories; nobody borrows another person's access. Work no person asked for
(the server's own token, a server nobody signs in to) acts as the owner.

`BEETHOVEN_GITHUB_APP` holds the app's credentials as JSON, the way GitHub's manifest conversion
returns them (`id`, `slug`, `client_id`, `client_secret`, `pem`, optional `name`, `owner.login`), or
the path of a file containing it. Its callback URL must be the server's URL plus `/github/authorized`,
and its setup URL the server's URL plus `/github/installed`.

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

### Insights

The **Insights** page measures how sessions go over the last 7, 30 or 90 days, quality first. Its
quality numbers cover the sessions people started (not automations, nor sessions agents spawned)
once they settle, meaning they ran and have nothing queued or running:

- **Cycles**: one prompt and its response. A session's cycles are its prompts that ran, the first
  request included.
- **First-try rate**: settled sessions that succeeded in one cycle. A session succeeds when its last
  cycle finished and none of its pull requests was closed without merging.
- **Success rate**, failures by reason, and stopped prompts.
- **Pull requests**: opened, merged, closed unmerged and still open, the median time from a session's
  start to its pull request and from opening to merging, and the cost per merged pull request (with
  the sessions agents spawned).
- **Usage**: sessions per day by outcome, automation runs, spend, and breakdowns by source, person
  and workspace.

Each session's header shows its cycles, the agents' working time and its pull requests' state, and
each automation shows how its recent runs went. Pull requests are tracked by polling GitHub every
ten minutes until they are merged or closed. Sessions are archived, never deleted, so the numbers
keep their history.

### Repository hooks

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
| `GET/POST /api/sessions` | list (`?team=` ids or `workspace`, `?mine=1`); create `{prompt, environment_id \| repos, model, effort, title, team_id, visibility}` |
| `GET /api/sessions/{id}` | session, prompts and pull requests |
| `POST /api/sessions/{id}/prompts` | queue `{content, model}` |
| | to attach files to either, send a multipart form: the JSON in a `request` field, each file in a `file` field |
| `GET /api/sessions/{id}/attachments/{aid}` | an attached file |
| `DELETE /api/sessions/{id}/prompts/{pid}` | cancel a prompt |
| `POST /api/sessions/{id}/stop` · `/archive` · `/pr` | stop; `{archived}`; `{repo, title, body, draft}` |
| `GET /api/auth` · `POST /api/auth/setup` · `/login` · `/logout` | who is signed in; create the first account; sign in and out |
| `GET /api/sessions/{id}/events` | server-sent events; `Last-Event-ID` resumes |
| `GET /api/events` | live state of every session |
| `/api/automations[/{id}[/run\|/runs]]` | CRUD, run now, run history |
| `POST /hooks/{id}` | fire an automation; `Authorization: Bearer <hook token>` |
| `GET/PUT/DELETE /api/secrets` | by scope; names only on read |
| `POST /api/secrets/import` · `GET /api/secrets/effective` | `.env` import; what a session receives |
| `/api/environments[/{id}]` | projects: CRUD; `team_id` on creation |
| `GET /api/teams` · `POST /api/teams` · `GET/PUT /api/teams/{id}` | teams (by id or short name) |
| `POST /api/teams/{id}/archive` · `/join` | `{archived}`; join an open team |
| `PUT/DELETE /api/teams/{id}/members/{account}` | `{role: lead \| member}`; remove, or leave |
| `GET /api/people` · `PUT /api/people/{id}` | everyone; `{role, disabled}` |
| `GET/POST /api/invites` · `DELETE /api/invites/{id}` | invitation links |
| `GET/POST /api/auth/invite` | an invitation's details; accept it `{token, name, email, password}` |
| `PUT /api/me` · `GET/POST /api/tokens` · `DELETE /api/tokens/{id}` | `{name, default_team_id}`; personal API tokens |
| `PUT /api/sessions/{id}/visibility` | `{visibility: workspace \| team \| private}` |
| `PUT /api/slack/channels/{team}/{channel}/team` | `{owner_team_id}`: the team a channel belongs to |
| `GET /api/audit?team=&before=` | changes to who may do what |
| `GET/PUT /api/settings` · `/api/credentials` | global settings; write-only credentials |
| `GET /api/github` | the GitHub App, the connected account and its installations |
| `POST /api/github/app` · `/app/created` · `DELETE /api/github/app` | register an app (manifest flow); forget it |
| `POST /api/github/authorize` · `/account` · `DELETE /api/github/account` | connect an account; disconnect it |
| `GET /api/slack` | the Slack app, its workspaces and channel defaults |
| `POST /api/slack/app` · `/install` · `/installed` | create the app; add it to a workspace |
| `DELETE /api/slack/app` · `/installations/{team}` · `/channels/{team}/{channel}` | remove them |
| `POST /slack/events` · `/slack/interactions` | Slack's events and clicks (signed by Slack) |
| `/api/mcp-servers[/{id}]` | CRUD; header and env values write-only |
| `GET /api/insights?days=&tz=` | quality, pull requests and usage of the last days |
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
internal/automation  schedules, webhooks, failure policy
internal/store       SQLite migrations and queries, environments, settings, encrypted secrets
internal/github      the GitHub App, its tokens and accounts, clone URLs, pull requests
internal/slack       the Slack app, its installations, and the bot
internal/insights    cycles, first-try rate, pull request outcomes and usage
internal/api         HTTP API, event streams, guards
web                  Preact UI, embedded in the binary
```

## License

[MIT](LICENSE). Beethoven was inspired by Ramp's [Inspect](https://builders.ramp.com/post/why-we-built-our-background-agent)
and [Open-Inspect](https://github.com/ColeMurray/background-agents).
