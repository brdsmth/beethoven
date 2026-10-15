# Beethoven

Background coding agents you run yourself. Give it a repository and a prompt — from the web UI, the
CLI, Slack, a cron schedule or a webhook — and an agent works on it in an isolated container while you do
something else. Everything it does streams live and is kept as a replayable log; follow-up prompts
queue on the same session and continue the same conversation; when the work is ready the agent opens
a pull request authored by you. Agents can split work across parallel child sessions.

A minimal reimplementation of [Open-Inspect](https://github.com/ColeMurray/background-agents) (itself
modelled on [Ramp's Inspect](https://builders.ramp.com/post/why-we-built-our-background-agent)): one
Go binary, SQLite, Docker and Claude Code: roughly 8,000 lines of Go and a small Preact UI, where the
original spans about 2,800 files across a dozen packages and several clouds.

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

**Sandbox.** One container per session behind a four-method interface: `Start` (create or resume),
`Exec`, `Stop` (keep the filesystem), `Destroy`. A sandbox stops after 10 idle minutes and resumes on
the next prompt with its workspace, installed dependencies and agent conversation intact. Nothing of
ours runs inside it: the server drives it with `exec`, so a finished process is a finished turn and
there are no bridges, heartbeats or connection watchdogs to keep alive. Stop kills the agent's whole
process tree inside the container.

**Agent.** A harness is two functions: build the command for a turn, and decode its output into
events. The Claude Code harness runs `claude -p --output-format stream-json` and resumes the previous
conversation with `--resume`. Adding another agent is one file.

**Tools.** The server hosts an MCP server that every agent connects to with its session's token:
`create_pull_request`, `spawn_session`, `send_prompt`, `get_session` (which can wait for a child to
finish), `list_sessions` and `stop_session`. Agents only reach their own direct children; children
nest at most two levels deep.

**Pull requests.** The agent commits; `create_pull_request` makes the server push the session's
branch (`beethoven/<id>`) from inside the sandbox with a token passed to that one command, then open
the PR through the GitHub API as the person who connected GitHub. Git credentials are never written
into the sandbox or given to the agent. Commits are authored by whoever sent the prompt (by default
the connected GitHub account, with its private `noreply` address) and committed by `Beethoven`.

**Automations.** A saved request: a prompt, a repository, and a cron schedule and/or a webhook.
Firing one submits a prompt like any other source; that prompt is the run. Runs don't overlap,
webhook redeliveries (same `Idempotency-Key` or `X-GitHub-Delivery`) fire once, the webhook body is
handed to the agent, and three failures in a row pause the automation. With "reuse", every run
continues one session.

**Environments.** A named, reusable workspace: an ordered list of up to ten repositories (the first is
the primary, each with its own base branch), setup and start scripts, its own secrets, and overrides
of the global settings (model, effort, instructions, sandbox image, CPUs, memory). A session started
from an environment copies its repositories and checks each out at `/workspace/<name>`; editing the
environment later doesn't move existing sessions, but its scripts, secrets and settings are read
live. A session can also target several repositories without an environment, one, or none.

**Secrets.** Environment variables for sandboxes, encrypted with AES-256-GCM under a key in the data
directory. Every secret has a scope — global, a repository, or an environment — and a session gets,
lowest precedence first: global secrets, then either its environment's secrets (an environment is a
boundary: its repositories' own secrets don't apply, and it can opt out of global ones) or its
repositories' secrets, the primary winning. `GET /api/secrets/effective` shows exactly what a session
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
connecting takes the browser to GitHub and back: create (only without a preconfigured app),
authorize, install. The server then clones and pushes with installation tokens it creates for one
repository at a time, valid for an hour, and only through installations the connected person can
access; public repositories the app isn't installed on are cloned anonymously. Pull requests,
branch and repository lists use the person's own token, which refreshes itself.

`BEETHOVEN_GITHUB_APP` holds the app's credentials as JSON, the way GitHub's manifest conversion
returns them (`id`, `slug`, `client_id`, `client_secret`, `pem`, optional `name`, `owner.login`), or
the path of a file containing it. Its callback URL must be the server's URL plus `/github/authorized`,
and its setup URL the server's URL plus `/github/installed`.

Until someone connects, a personal access token is used instead: one saved under **Settings ›
GitHub**, `GITHUB_TOKEN`, or the GitHub CLI's (`gh auth token`).

### Slack

Mention **@beethoven** in a channel, or send it a direct message, and it works on the request in a
session and answers in the thread: 👀 on the request, a status reply with **Open in Beethoven** and
**Stop**, then the agent's answer and its pull requests, and ✅ or ❌. The thread is the session:
mention the bot in it again (any reply, in a direct message) to queue a follow-up, which carries what
was said in the thread since as context. A request works on the repositories (`owner/repo`) or the
environment it names, else on the channel's default; otherwise the bot asks, and the answer can
become the channel's default. Full members of the workspace can start sessions; guests and people
from other organizations in shared channels can't. Commits are authored by the Slack user, with the
email on their Slack profile.

Like GitHub, Slack is reached through an app, which is either:

- **Preconfigured**: an operator's app, given in `BEETHOVEN_SLACK_APP` as JSON (`app_id`,
  `client_id`, `client_secret`, `signing_secret`) or a file holding it. People click **Add to Slack**.
  Its event and interactivity URLs are the server's URL plus `/slack/events` and
  `/slack/interactions`, and its redirect URL the server's URL plus `/slack/installed`.
- **Created from the server**: paste an [app configuration token](https://api.slack.com/apps) (under
  *Your App Configuration Tokens*) in **Settings › Slack**, and the server creates its app with
  Slack's manifest API, then sends you on to add it to the workspace. The token is used once and not
  stored.

Slack only delivers events to a public HTTPS address, so the server needs one; for local testing, a
tunnel such as `cloudflared tunnel --url http://localhost:7070` works, with its host in
`BEETHOVEN_HOSTS`.

### Repository hooks

- `.beethoven/setup.sh` runs once per sandbox after the clone (dependencies, builds). Failure is a
  warning.
- `.beethoven/start.sh` runs every time the sandbox starts (services). Failure fails the prompt.

## Configuration

| Variable | Default | |
|---|---|---|
| `BEETHOVEN_ADDR` | `127.0.0.1:7070` | listen address |
| `BEETHOVEN_DATA` | `~/.beethoven` | database and encryption key |
| `BEETHOVEN_TOKEN` | — | require a bearer token on the API |
| `BEETHOVEN_HOSTS` | — | extra `Host` names to accept (for access through a proxy) |
| `BEETHOVEN_IMAGE` | `beethoven-sandbox:latest` | sandbox image |
| `BEETHOVEN_SANDBOX_URL` | `http://host.docker.internal:<port>` | the server as seen from sandboxes |
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
reach (for example `BEETHOVEN_ADDR=0.0.0.0:7070` with `BEETHOVEN_TOKEN` set).

## Security model

Single user, trusted repositories. The API listens on localhost and rejects cross-site requests and
foreign `Host` headers (CSRF and DNS rebinding); set `BEETHOVEN_TOKEN` before exposing it. Agents run
with permissions bypassed inside their container, see the sandbox secrets, and can reach the network.
Their tool token only acts on their own session and its children. GitHub tokens reach a sandbox only
for the clone and push commands the server runs, each limited to one repository for an hour. GitHub
returns the browser to `/github/<step>` without the API token, so those pages only redirect to the UI,
which completes each step through the API; a single-use state ties each return to the request that
started it; Slack's installation works the same way. Requests from Slack must carry a valid signature
from the last five minutes, a redelivered event is acted on once, the bot ignores bots (itself
included), and what was said in a thread reaches the agent marked as context, not instructions.

## API

| | |
|---|---|
| `GET/POST /api/sessions` | list; create `{prompt, environment_id \| repos, model, effort, title}` |
| `GET /api/sessions/{id}` | session and prompts |
| `POST /api/sessions/{id}/prompts` | queue `{content, model}` |
| `DELETE /api/sessions/{id}/prompts/{pid}` | cancel a prompt |
| `POST /api/sessions/{id}/stop` · `/archive` · `/pr` | stop; `{archived}`; `{repo, title, body, draft}` |
| `GET /api/sessions/{id}/events` | server-sent events; `Last-Event-ID` resumes |
| `GET /api/events` | live state of every session |
| `/api/automations[/{id}[/run\|/runs]]` | CRUD, run now, run history |
| `POST /hooks/{id}` | fire an automation; `Authorization: Bearer <hook token>` |
| `GET/PUT/DELETE /api/secrets` | by scope; names only on read |
| `POST /api/secrets/import` · `GET /api/secrets/effective` | `.env` import; what a session receives |
| `/api/environments[/{id}]` | CRUD |
| `GET/PUT /api/settings` · `/api/credentials` | global settings; write-only credentials |
| `GET /api/github` | the GitHub App, the connected account and its installations |
| `POST /api/github/app` · `/app/created` · `DELETE /api/github/app` | register an app (manifest flow); forget it |
| `POST /api/github/authorize` · `/account` · `DELETE /api/github/account` | connect an account; disconnect it |
| `GET /api/slack` | the Slack app, its workspaces and channel defaults |
| `POST /api/slack/app` · `/install` · `/installed` | create the app; add it to a workspace |
| `DELETE /api/slack/app` · `/installations/{team}` · `/channels/{team}/{channel}` | remove them |
| `POST /slack/events` · `/slack/interactions` | Slack's events and clicks (signed by Slack) |
| `/api/mcp-servers[/{id}]` | CRUD; header and env values write-only |
| `GET /api/repos[/{owner}/{name}/branches]` | GitHub repositories and branches, for pickers |
| `/mcp` | agent tools (session tokens) |

## Development

```sh
make test-short       # unit tests, no Docker
make test             # plus the Docker end-to-end test
make fake-image       # a sandbox image whose `claude` is a script: no API calls
BEETHOVEN_IMAGE=beethoven-fake:latest bin/beethoven serve
```

The fake agent understands `COMMIT`, `SLEEP n` and `FAIL` in prompts.

```
cmd/beethoven        server and CLI
internal/session     sessions: queue, event log, sandbox lifecycle, PRs, boot script
internal/sandbox     Provider interface, Docker implementation, sandbox Dockerfile
internal/agent       Harness interface, Claude Code implementation
internal/tools       MCP tool server
internal/automation  schedules, webhooks, failure policy
internal/store       SQLite migrations and queries, environments, settings, encrypted secrets
internal/github      the GitHub App, its tokens and accounts, clone URLs, pull requests
internal/slack       the Slack app, its installations, and the bot
internal/api         HTTP API, event streams, guards
web                  Preact UI, embedded in the binary
```

## Not (yet) here

Compared with Open-Inspect: GitHub and Linear bots (the `Notifier` interface and continuation keys
are their extension points); in Slack, picking a repository with a model, live progress, images and
App Home preferences, and Socket Mode for servers without a public address; sandbox providers other than Docker; prebuilt images and warming a
sandbox while you type; automation fan-out across repositories; users, teams and permissions;
presence; in-browser terminal and editor; tunnelled ports; memory, skills, budgets and analytics.
