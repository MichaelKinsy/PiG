#!/usr/bin/env bash
# Prove a test run leaves the agent directories its environment names untouched.
#   agent-dir-guard.sh seed <root>    create <root>/{agent,pi-agent,pig-home,pi-home} holding sentinel credentials, settings and trust files, record their manifest, and print `export` lines for the four selecting variables
#   agent-dir-guard.sh check <root>   fail when any file or directory under <root> was added, removed, changed or modified since seed, including a lock directory a test created and removed again
# A lane exports PIG_CODING_AGENT_DIR for its own pig process and every child inherits it, so a test that does not replace it writes the lane's real credentials. `make test` runs under seeded directories, which turns that leak into a named failure.
set -euo pipefail

usage() { echo "usage: agent-dir-guard.sh seed|check <root>" >&2; exit 2; }
[[ $# -eq 2 ]] || usage
mode=$1
root=$2
manifest="$root.manifest"

# The sentinel is a plausible credential store, so a test that logs out, refreshes or rewrites it changes the bytes.
sentinel_auth='{"agent-dir-guard":{"type":"api_key","key":"sentinel-not-a-secret"}}'

# Hash with sha256sum, or shasum on macOS. Without either the guard cannot prove anything, so seed and check fail instead of passing.
if command -v sha256sum >/dev/null 2>&1; then
  hasher=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  hasher=(shasum -a 256)
else
  echo "[agent-dir-guard] neither sha256sum nor shasum is on PATH" >&2
  exit 2
fi

# One line per entry: the content hash of each regular file and a type marker for every directory and symlink, so a test that only creates a session directory also changes the manifest.
digest() {
  (
    cd "$root"
    find . -mindepth 1 ! -type f -print | LC_ALL=C sort | sed 's/^/non-file  /'
    find . -type f -print0 | LC_ALL=C sort -z | xargs -0 -r "${hasher[@]}"
  )
}

case "$mode" in
  seed)
    for dir in agent pi-agent pig-home pi-home; do
      mkdir -p "$root/$dir"
      printf '%s\n' "$sentinel_auth" >"$root/$dir/auth.json"
      printf '{"theme":"dark"}\n' >"$root/$dir/settings.json"
      printf '{}\n' >"$root/$dir/trust.json"
      printf '{"providers":{}}\n' >"$root/$dir/models.json"
    done
    chmod -R go-rwx "$root"
    digest >"$manifest"
    # Backdate the tree, then the manifest, so check can list every entry modified after seed with find -newer whatever the timestamp granularity.
    find "$root" -exec touch -t 200001010000 {} +
    touch -t 200001010001 "$manifest"
    printf 'export PIG_CODING_AGENT_DIR=%q PI_CODING_AGENT_DIR=%q PIG_HOME=%q PI_HOME=%q\n' "$root/agent" "$root/pi-agent" "$root/pig-home" "$root/pi-home"
    printf 'unset PIG_CODING_AGENT_SESSION_DIR PI_CODING_AGENT_SESSION_DIR\n'
    ;;
  check)
    [[ -f $manifest ]] || { echo "[agent-dir-guard] no manifest for $root; run seed first" >&2; exit 2; }
    # Capture the digest first: a failure inside a process substitution is invisible to set -e and would compare an empty listing.
    current=$(digest)
    drift=$(diff "$manifest" <(printf '%s\n' "$current") || true)
    # A directory whose entries were created and removed again, or a file rewritten with the same bytes, keeps its digest but not its modification time.
    touched=$(find "$root" -newer "$manifest" -print)
    if [[ -n $drift || -n $touched ]]; then
      echo "[agent-dir-guard] a test run modified the agent directories named by PIG_CODING_AGENT_DIR, PI_CODING_AGENT_DIR, PIG_HOME or PI_HOME (paths relative to $root):" >&2
      {
        [[ -z $drift ]] || printf '%s\n' "$drift" | awk '/^[<>]/ {print "  " ($1 == "<" ? "removed or changed:" : "added or changed:") " " $3}'
        [[ -z $touched ]] || printf '%s\n' "$touched" | while IFS= read -r path; do printf '  modified: .%s\n' "${path#"$root"}"; done
      } | LC_ALL=C sort -u >&2
      echo "[agent-dir-guard] every test that reaches the agent directory must set PIG_CODING_AGENT_DIR, PI_CODING_AGENT_DIR, PIG_HOME and PI_HOME to t.TempDir(); a package's TestMain gets this from testenv.ScopeTempDir." >&2
      exit 1
    fi
    ;;
  *) usage ;;
esac
