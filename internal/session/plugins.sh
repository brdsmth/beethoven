# Claude Code plugins for the agent, prepended to boot.sh.
#
# Env:
#   PLUGINS  one per line: "<plugin>@<marketplace source>", the source as
#            owner/repo, a git URL or an https URL

# plugins_install installs each plugin once per sandbox, adding its
# marketplace first. A failure is a warning, retried on the next start.
plugins_install() {
  [ -n "${PLUGINS:-}" ] || return 0
  mkdir -p "$HOME/.beethoven"
  printf '%s\n' "$PLUGINS" | while read -r plugin; do
    [ -n "$plugin" ] || continue
    marker="$HOME/.beethoven/plugin-$(printf '%s' "$plugin" | cksum | cut -d' ' -f1)"
    [ -f "$marker" ] && continue
    phase plugins
    if claude plugin install "${plugin%%@*}" --marketplace "${plugin#*@}" </dev/null; then
      touch "$marker"
    else
      echo "::warn couldn't install the Claude Code plugin $plugin"
    fi
  done
}
