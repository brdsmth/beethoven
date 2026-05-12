# beethoven

An AI-native workspace that uses git worktrees to create isolated task environments for AI-assisted development.

Each task gets its own branch, worktree, run directory, and artifact directory — keeping work scoped and easy to review.

## Prerequisites

- Node.js >= 20
- [pnpm](https://pnpm.io) >= 11
- git

## Setup

```bash
# Install dependencies
pnpm install

# Start the development server
pnpm dev
```

Or use the start script:

```bash
./scripts/dev/start.sh
```

## Task Workflow

### 1. Create a new task

```bash
./scripts/dev/new-task.sh <task-name>
```

This creates:
- A git worktree at `~/workspace/workspaces/active/workspaces/<task-name>`
- A run directory at `~/workspace/runs/active/run-<timestamp>` with scaffolded files
- An artifact directory at `~/workspace/artifacts/run-<timestamp>`

### 2. Write a prompt

Edit `~/workspace/runs/active/run-<timestamp>/prompt.md` to describe the task.

### 3. Start working

```bash
cd ~/workspace/workspaces/active/workspaces/<task-name>
# open your editor or start Claude
```

## Directory Structure

```
~/workspace/
├── workspaces/active/workspaces/   # isolated git worktrees per task
│   └── <task-name>/                # branch + working tree for the task
│
├── runs/active/                    # run metadata and artifacts
│   └── run-<timestamp>/
│       ├── prompt.md               # task description
│       ├── plan.md                 # planning output
│       ├── review.md               # review findings
│       ├── execution-log.md        # commands and outputs
│       ├── metadata.json           # run metadata
│       └── RUN.md                  # freeform notes
│
└── artifacts/                      # generated assets per run
    └── run-<timestamp>/
        ├── screenshots/
        ├── reports/
        └── previews/
```

## Scripts

| Script | Description |
|--------|-------------|
| `scripts/dev/start.sh` | Start the development server |
| `scripts/dev/new-task.sh <name>` | Create a new isolated task environment |
