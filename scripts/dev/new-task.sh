#!/usr/bin/env bash

# Added: fail immediately on errors
set -e

# Added: ensure task name exists
if [ -z "$1" ]; then
  echo "Usage: ./scripts/dev/new-task.sh <task-name>"
  exit 1
fi

# Added: task name
TASK_NAME="$1"

# Added: workspace root
WORKSPACE_ROOT="$HOME/workspace"

# Added: orchestration paths
WORKTREES_DIR="$WORKSPACE_ROOT/workspaces/active/workspaces"
RUNS_DIR="$WORKSPACE_ROOT/runs/active"
ARTIFACTS_DIR="$WORKSPACE_ROOT/artifacts"

# Added: unique timestamp run id
RUN_ID="run-$(date +%Y%m%d-%H%M%S)"

# Added: derived paths
WORKTREE_PATH="$WORKTREES_DIR/$TASK_NAME"
RUN_PATH="$RUNS_DIR/$RUN_ID"
ARTIFACT_PATH="$ARTIFACTS_DIR/$RUN_ID"

# Added: create isolated git worktree
git worktree add "$WORKTREE_PATH" -b "$TASK_NAME"

# Added: create run directory
mkdir -p "$RUN_PATH"

# Added: create artifact directories
mkdir -p \
  "$ARTIFACT_PATH/screenshots" \
  "$ARTIFACT_PATH/reports" \
  "$ARTIFACT_PATH/previews"

# Added: create run files
touch \
  "$RUN_PATH/prompt.md" \
  "$RUN_PATH/plan.md" \
  "$RUN_PATH/review.md" \
  "$RUN_PATH/execution-log.md" \
  "$RUN_PATH/RUN.md"

# Added: create metadata
cat > "$RUN_PATH/metadata.json" <<EOF
{
  "runId": "$RUN_ID",
  "taskName": "$TASK_NAME",
  "worktreePath": "$WORKTREE_PATH",
  "createdAt": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
  "status": "created"
}
EOF

# Added: helpful output
echo ""
echo "Task created successfully"
echo ""
echo "Worktree:"
echo "$WORKTREE_PATH"
echo ""
echo "Run:"
echo "$RUN_PATH"
echo ""
echo "Artifacts:"
echo "$ARTIFACT_PATH"
echo ""
echo "Next steps:"
echo "1. Edit prompt.md"
echo "2. cd into worktree"
echo "3. Start claude"
echo ""
