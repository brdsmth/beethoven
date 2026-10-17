# Capabilities: development toolchains installed on demand (see package
# capability). The server sends the catalog on stdin as a tar archive with a
# directory per capability: name, hash, install.sh, prepare.sh (optional),
# detect.sh, SKILL.md, secrets and env. Lines starting with "::capability",
# "::suggest", "::env" and "::recorded" tell the server what happened.
#
# Env:
#   CAPABILITIES  the ids the server enabled, space separated
#   REPOS         as for boot.sh; only each line's directory is used
#   WORKDIR       prepared instead of repositories when there are none
#   CAP_RECORD    for cap_add: the file to record added ids in
#   WORKSPACE     where repositories are cloned; /workspace unless testing
cap_dir="$HOME/.beethoven/catalog"
cap_done="$HOME/.beethoven" # markers for what was installed in this sandbox

cap_unpack() {
  rm -rf "$cap_dir" && mkdir -p "$cap_dir" && tar -x -f - -C "$cap_dir"
}

# cap_repos lists the directories capabilities prepare: each repository, or
# the workspace when there are none.
cap_repos() {
  if [ -z "${REPOS:-}" ]; then echo "${WORKDIR:-${WORKSPACE:-/workspace}}"; return; fi
  printf '%s\n' "$REPOS" | while read -r cap_r _; do if [ -n "$cap_r" ]; then echo "${WORKSPACE:-/workspace}/$cap_r"; fi; done
}

# cap_wanted lists the ids to set up, once each: the server's, then those in
# each repository's .beethoven/capabilities (any whitespace separates ids, #
# starts a comment).
cap_wanted() {
  {
    for cap_id in ${CAPABILITIES:-}; do echo "$cap_id"; done
    for cap_r in $(cap_repos); do
      if [ -f "$cap_r/.beethoven/capabilities" ]; then sed 's/#.*//' "$cap_r/.beethoven/capabilities" | tr -s ' \t\r' '\n\n\n'; fi
    done
  } | awk 'NF && !seen[$0]++'
}

# cap_install installs one capability unless this sandbox already has this
# version of it, prepares each repository, writes its skill, and checks its
# secrets. A failure is a warning. cap_ok says whether it is ready.
cap_install() {
  cap_id=$1 cap_d="$cap_dir/$1" cap_ok=
  if [ ! -f "$cap_d/install.sh" ]; then
    echo "::warn unknown capability \"$cap_id\"; see Settings for the capabilities available"
    return 0
  fi
  cap_name=$(cat "$cap_d/name") cap_hash=$(cat "$cap_d/hash")
  if [ ! -f "$cap_done/capability-$cap_id-$cap_hash" ]; then
    echo "::phase capability $cap_id"
    if (cd "$HOME" && sh -e "$cap_d/install.sh"); then
      touch "$cap_done/capability-$cap_id-$cap_hash"
    else
      echo "::warn the $cap_name capability failed to install"
      return 0
    fi
  fi
  if [ -f "$cap_d/prepare.sh" ]; then
    for cap_r in $(cap_repos); do
      cap_mark="$cap_done/capability-$cap_id-$cap_hash-$(basename "$cap_r")"
      if [ -d "$cap_r" ] && [ ! -f "$cap_mark" ]; then
        if (cd "$cap_r" && sh -e "$cap_d/prepare.sh"); then touch "$cap_mark"
        else echo "::warn the $cap_name capability couldn't install the dependencies in $cap_r"; fi
      fi
    done
  fi
  mkdir -p "$HOME/.claude/skills/$cap_id" && cp "$cap_d/SKILL.md" "$HOME/.claude/skills/$cap_id/SKILL.md"
  while read -r cap_var cap_need cap_why; do
    if [ -n "$cap_var" ] && [ "$cap_need" = required ]; then
      eval "cap_val=\${$cap_var:-}"
      if [ -z "$cap_val" ]; then echo "::warn the $cap_name capability needs $cap_var, $cap_why: add it as a secret"; fi
    fi
  done < "$cap_d/secrets"
  while IFS= read -r cap_kv; do
    if [ -n "$cap_kv" ]; then eval "echo \"::env $cap_kv\""; fi
  done < "$cap_d/env"
  echo "::capability $cap_id"
  cap_ok=1
}

# cap_setup installs every wanted capability, then suggests others that the
# repositories look like they need.
cap_setup() {
  cap_list=" "
  for cap_id in $(cap_wanted); do
    cap_install "$cap_id"
    cap_list="$cap_list$cap_id "
  done
  for cap_d in "$cap_dir"/*/; do
    cap_id=$(basename "$cap_d")
    case "$cap_list" in *" $cap_id "*) continue ;; esac
    for cap_r in $(cap_repos); do
      if [ -d "$cap_r" ] && (cd "$cap_r" && sh "$cap_d/detect.sh") >/dev/null 2>&1; then
        echo "::suggest $cap_id"
        break
      fi
    done
  done
}

# cap_add installs the capabilities in CAPABILITIES now and records each
# that installed in CAP_RECORD, if set.
cap_add() {
  for cap_add_id in ${CAPABILITIES:-}; do
    cap_install "$cap_add_id"
    if [ -n "$cap_ok" ] && [ -n "${CAP_RECORD:-}" ]; then
      mkdir -p "$(dirname "$CAP_RECORD")" && touch "$CAP_RECORD"
      if ! sed 's/#.*//' "$CAP_RECORD" | tr -s ' \t\r' '\n\n\n' | grep -qx "$cap_add_id"; then echo "$cap_add_id" >> "$CAP_RECORD"; fi
      echo "::recorded $CAP_RECORD"
    fi
  done
}

# cap_detect suggests every capability the repositories look like they need.
cap_detect() {
  for cap_d in "$cap_dir"/*/; do
    for cap_r in $(cap_repos); do
      if [ -d "$cap_r" ] && (cd "$cap_r" && sh "$cap_d/detect.sh") >/dev/null 2>&1; then
        echo "::suggest $(basename "$cap_d")"
        break
      fi
    done
  done
}
