# Prepares /workspace inside a sandbox. Runs on every sandbox start.
# Lines starting with "::phase" and "::warn" are read by the server.
#
# Env:
#   REPOS         one line per repository: "<dir> <clone url> <base branch or ->", primary first
#   WORK_BRANCH   the session's branch, created in every repository
#   WORKDIR       where the agent works, and where environment scripts run
#   GH_TOKENS     clone credentials, one line per repository: "<dir> <token>"; never
#                 written to disk or shown to scripts
#   ENV_SETUP     environment setup script: once per sandbox, and again when it changes (ENV_SETUP_ID)
#   ENV_START     environment start script: on every start
#   CAPABILITIES  capabilities to install, besides those the repositories name or
#                 look like they need; the catalog arrives on stdin (capabilities.sh,
#                 prepended)
#   PLUGINS       Claude Code plugins to install (plugins.sh, prepended)
#   BOOT_STAGE    "install": stop once the capabilities are installed, before any
#                 of the repositories' code runs; "rest": carry on from there.
#                 Unset: both.
set -eu
phase() { echo "::phase $*"; }
git_auth() {
  git -c credential.helper= \
      -c 'credential.helper=!f() { test "$1" = get && echo username=x-access-token && echo "password=${GH_TOKEN:-}"; }; f' "$@"
}
token_for() { printf '%s\n' "${GH_TOKENS:-}" | while read -r dir tok; do if [ "$dir" = "$1" ]; then echo "$tok"; fi; done; }
dirs() { printf '%s\n' "${REPOS:-}" | while read -r dir _; do [ -n "$dir" ] && echo "$dir"; done; }
done_dir="$HOME/.beethoven" # markers for setup that already ran in this sandbox
mkdir -p "$done_dir"
cap_unpack

mkdir -p /workspace
cd /workspace
if [ "${BOOT_STAGE:-}" != rest ]; then
if [ -z "${REPOS:-}" ] && [ ! -d .git ]; then
  git init -q -b "$WORK_BRANCH" # a scratch workspace
fi
printf '%s\n' "${REPOS:-}" | while read -r dir url base; do
  [ -n "$dir" ] || continue
  GH_TOKEN=$(token_for "$dir"); export GH_TOKEN
  if [ ! -d "$dir/.git" ]; then
    phase clone "$dir"
    rm -rf "$dir" # leftovers of an interrupted clone
    # A full clone: the sandbox keeps no credentials, so a partial clone
    # couldn't fetch the files it skipped when the agent diffs a branch,
    # reads history or checks out another commit.
    if [ "$base" = "-" ]; then
      git_auth clone "$url" "$dir"
    else
      git_auth clone --branch "$base" "$url" "$dir"
    fi
    git -C "$dir" checkout -q -b "$WORK_BRANCH"
  else
    phase fetch "$dir"
    if [ "$(git -C "$dir" config --get remote.origin.promisor || true)" = true ]; then
      # Made by an older, partial clone: fetch what it skipped while the
      # credentials are here, then stop treating origin as a promisor.
      git -C "$dir" config --unset-all remote.origin.partialclonefilter || true
      if git_auth -C "$dir" fetch --quiet --refetch origin; then
        git -C "$dir" config --unset-all remote.origin.promisor
      else
        echo "::warn git fetch failed for $dir; continuing with the existing, partial checkout"
      fi
    else
      git_auth -C "$dir" fetch --quiet origin || echo "::warn git fetch failed for $dir; continuing with the existing checkout"
    fi
  fi
done
unset GH_TOKEN GH_TOKENS

# Capabilities: toolchains the server enabled or the repositories name or
# look like they need, before the hooks and scripts that may need them.
cap_setup
fi
unset GH_TOKEN GH_TOKENS # also in the rest stage, which doesn't clone
if [ "${BOOT_STAGE:-}" = install ]; then exit 0; fi
# Plugins for the agent, which the setup scripts may use too.
plugins_install
# The repositories' dependencies, then their hooks: their own code.
cap_prepare

# Repository hooks: .beethoven/setup.sh provisions once per sandbox and may
# fail with a warning; .beethoven/start.sh runs on every start and must pass.
for dir in $(dirs); do
  if [ -f "$dir/.beethoven/setup.sh" ] && [ ! -f "$done_dir/setup-$dir" ]; then
    phase setup "$dir"
    if (cd "$dir" && sh .beethoven/setup.sh); then touch "$done_dir/setup-$dir"; else echo "::warn $dir/.beethoven/setup.sh failed"; fi
  fi
done
if [ -n "${ENV_SETUP:-}" ] && [ ! -f "$done_dir/env-setup-${ENV_SETUP_ID:-}" ]; then
  phase setup
  if (cd "$WORKDIR" && sh -ec "$ENV_SETUP"); then touch "$done_dir/env-setup-${ENV_SETUP_ID:-}"; else echo "::warn the environment's setup script failed"; fi
fi
for dir in $(dirs); do
  if [ -f "$dir/.beethoven/start.sh" ]; then
    phase start "$dir"
    (cd "$dir" && sh .beethoven/start.sh)
  fi
done
if [ -n "${ENV_START:-}" ]; then
  phase start
  (cd "$WORKDIR" && sh -ec "$ENV_START")
fi
phase ready
