# Capabilities: development toolchains installed on demand (see package
# capability). The server sends the catalog on stdin as a tar archive with a
# directory per capability: name, hash, install.sh, prepare.sh (optional),
# detect.sh, paths, SKILL.md, secrets and env. Lines starting with
# "::capability", "::suggest", "::repo", "::cache", "::env" and "::recorded"
# tell the server what happened.
#
# Env:
#   CAPABILITIES  the ids the server enabled, space separated
#   REPOS         as for boot.sh; only each line's directory is used
#   WORKDIR       prepared instead of repositories when there are none
#   CAP_RECORD    for cap_add: the file to record added ids in
#   CAP_EXPORT    set: pack fresh installs for the server (see cap_export)
#   CAP_CACHED    the "id-hash" installs the server unpacked beforehand
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
# version of it and writes its skill. A failure is a
# warning. cap_ok says whether it is ready. With CAP_EXPORT set, a fresh
# install is packed for the server to keep (CAP_CACHED lists the "id-hash"
# installs it unpacked instead, which needn't be packed again).
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
      cap_export
    else
      echo "::warn the $cap_name capability failed to install"
      return 0
    fi
  fi
  mkdir -p "$HOME/.claude/skills/$cap_id" && cp "$cap_d/SKILL.md" "$HOME/.claude/skills/$cap_id/SKILL.md"
  while IFS= read -r cap_kv; do
    if [ -n "$cap_kv" ]; then eval "echo \"::env $cap_kv\""; fi
  done < "$cap_d/env"
  touch "$cap_done/capabilities"
  if ! grep -qx "$cap_id" "$cap_done/capabilities"; then echo "$cap_id" >> "$cap_done/capabilities"; fi
  echo "::capability $cap_id"
  cap_ok=1
}

# cap_export packs what the capability just installed, when the server asks
# for it, and says where with the archive's checksum: "::cache <id> <hash>
# <machine> <sha256> <file>".
cap_export() {
  if [ -z "${CAP_EXPORT:-}" ] || [ ! -s "$cap_d/paths" ]; then return 0; fi
  case " ${CAP_CACHED:-} " in *" $cap_id-$cap_hash "*) return 0 ;; esac
  cap_f="$cap_done/export/$cap_id.tar.gz"
  mkdir -p "$cap_done/export"
  # shellcheck disable=SC2046 # the paths have no spaces
  if (cd "$HOME" && tar -czf "$cap_f" $(cat "$cap_d/paths")) 2>/dev/null; then
    echo "::cache $cap_id $cap_hash $(uname -m) $(sha256sum "$cap_f" | cut -d' ' -f1) $cap_f"
  else
    rm -f "$cap_f"
  fi
}

# cap_prepare checks the secrets of every capability this sandbox has and
# installs each repository's dependencies for it, once per capability
# version and repository. It runs the repositories' own code (package
# scripts), so it comes after cap_setup, which has no secrets when it packs
# installs.
cap_prepare() {
  if [ ! -f "$cap_done/capabilities" ]; then return 0; fi
  for cap_id in $(cat "$cap_done/capabilities"); do cap_prepare_one "$cap_id"; done
}

cap_prepare_one() {
  cap_d="$cap_dir/$1"
  if [ ! -f "$cap_d/name" ]; then return 0; fi
  cap_name=$(cat "$cap_d/name") cap_hash=$(cat "$cap_d/hash")
  while read -r cap_var cap_need cap_why; do
    if [ -n "$cap_var" ] && [ "$cap_need" = required ]; then
      eval "cap_val=\${$cap_var:-}"
      if [ -z "$cap_val" ]; then echo "::warn the $cap_name capability needs $cap_var, $cap_why: add it as a secret"; fi
    fi
  done < "$cap_d/secrets"
  if [ ! -f "$cap_d/prepare.sh" ]; then return 0; fi
  for cap_r in $(cap_repos); do
    cap_mark="$cap_done/capability-$1-$cap_hash-$(basename "$cap_r")"
    if [ -d "$cap_r" ] && [ ! -f "$cap_mark" ]; then
      echo "::phase prepare $1"
      if (cd "$cap_r" && sh -e "$cap_d/prepare.sh"); then touch "$cap_mark"
      else echo "::warn the $cap_name capability couldn't install the dependencies in $cap_r"; fi
    fi
  done
}

# cap_setup installs every wanted capability, and every other one that a
# repository looks like it needs. "::repo <dir> <id>" says which
# capabilities each repository named or looked like it needed.
cap_setup() {
  for cap_id in $(cap_wanted); do cap_install "$cap_id"; done
  for cap_r in $(cap_repos); do
    if [ -f "$cap_r/.beethoven/capabilities" ]; then
      for cap_id in $(sed 's/#.*//' "$cap_r/.beethoven/capabilities"); do
        if [ -f "$cap_dir/$cap_id/install.sh" ]; then echo "::repo $(basename "$cap_r") $cap_id"; fi
      done
    fi
  done
  for cap_d in "$cap_dir"/*/; do
    cap_id=$(basename "$cap_d")
    for cap_r in $(cap_repos); do
      if [ -d "$cap_r" ] && (cd "$cap_r" && sh "$cap_d/detect.sh") >/dev/null 2>&1; then
        echo "::repo $(basename "$cap_r") $cap_id"
        cap_install "$cap_id"
      fi
    done
  done
}

# cap_add installs the capabilities in CAPABILITIES now, with their
# dependencies, and records each that installed in CAP_RECORD, if set.
cap_add() {
  for cap_add_id in ${CAPABILITIES:-}; do
    cap_install "$cap_add_id"
    if [ -n "$cap_ok" ]; then cap_prepare_one "$cap_add_id"; fi
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
