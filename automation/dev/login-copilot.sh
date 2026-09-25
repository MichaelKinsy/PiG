#!/usr/bin/env bash
# automation/dev/login-copilot.sh: interactive helper for setting up a pig
# auth.json so live tests have a credential to use.
#
# Live integration tests look for an OAuth credential at
# $PIG_HOME/agent/auth.json (or ~/.pig/agent/auth.json). This script
# runs the github-copilot device-code flow if no credential is
# present, or prints the existing credential's expiry if one exists.
#
# Usage:
#   automation/dev/login-copilot.sh           # login if missing, else show status
#   automation/dev/login-copilot.sh --force   # re-run device flow even if creds exist
#   automation/dev/login-copilot.sh --status  # only print status; never prompt
#
# After this, run:
#   go test -tags="integration live" ./tests/integration/...

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PIG_HOME="${PIG_HOME:-$HOME/.pig}"
AUTH_FILE="$PIG_HOME/agent/auth.json"

show_status() {
  if [[ ! -f "$AUTH_FILE" ]]; then
    echo "❌ no auth.json at $AUTH_FILE"
    return 1
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    echo "✅ auth.json present at $AUTH_FILE (install python3 for expiry detail)"
    return 0
  fi
  python3 - "$AUTH_FILE" <<'PY'
import json, sys, time
path = sys.argv[1]
with open(path) as f:
    auth = json.load(f)
if not auth:
    print(f"❌ {path} is empty")
    sys.exit(1)
print(f"✅ {path} contains {len(auth)} provider(s):")
for prov, cred in auth.items():
    typ = cred.get("type", "?")
    line = f"   · {prov}  type={typ}"
    if "expires" in cred:
        exp = cred["expires"] / 1000
        delta = exp - time.time()
        status = "valid" if delta > 0 else "EXPIRED"
        hours = delta / 3600
        line += f"  ({status}, {hours:+.1f}h)"
    print(line)
PY
}

force=0
status_only=0
for arg in "$@"; do
  case "$arg" in
    --force) force=1 ;;
    --status) status_only=1 ;;
    -h|--help)
      sed -n '2,17p' "$0"
      exit 0
      ;;
    *)
      echo "unknown arg: $arg" >&2
      exit 2
      ;;
  esac
done

if [[ $status_only -eq 1 ]]; then
  show_status
  exit $?
fi

if [[ -f "$AUTH_FILE" && $force -eq 0 ]]; then
  show_status
  echo
  echo "Use --force to re-run the device flow."
  exit 0
fi

# Build pig if needed.
BIN="$REPO_ROOT/bin/pig"
mkdir -p "$REPO_ROOT/bin"
echo "Building pig…"
(cd "$REPO_ROOT" && go build -o "$BIN" ./cmd/pig)

echo
echo "Running github-copilot device-code flow."
echo "Follow the prompts (open the URL, paste the code in your browser)."
echo
"$BIN" login github-copilot

echo
show_status
