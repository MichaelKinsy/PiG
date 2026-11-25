#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Build and configure PiG's S3/R2-backed GOCACHEPROG helper (automation/ci/gocacheprog).
#
# Modes & security properties:
#   - Remote mode is enabled ONLY when a bucket is configured AND either credentials
#     (PIG_GOCACHE_ACCESS_KEY_ID + PIG_GOCACHE_SECRET_ACCESS_KEY, or AWS_ACCESS_KEY_ID +
#     AWS_SECRET_ACCESS_KEY) are present or PIG_GOCACHE_ANONYMOUS=1 (public-read bucket).
#   - When remote bucket or credentials are absent (fork PRs, local dev, unconfigured repos),
#     the script falls back cleanly to Go's default local disk cache and leaves GOCACHEPROG
#     unset (exit 0) — it NEVER fails a build because remote cache config is absent or unreachable.
#   - Cache-poisoning protection: writes are enabled ONLY when PIG_GOCACHE_MODE=readwrite
#     (or --mode=readwrite). Any pull_request / pull_request_target / fork context, or an
#     explicit readonly request, forces --readonly so untrusted PR code can never write
#     arbitrary build or test outputs into the shared bucket.
#
# Usage:
#   automation/ci/setup-gocacheprog.sh [--mode auto|readonly|readwrite|local] [--print-env] [--github-env FILE]
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)

MODE="${PIG_GOCACHE_MODE:-auto}"
PRINT_ENV=0
GITHUB_ENV_FILE="${GITHUB_ENV:-}"
GITHUB_OUTPUT_FILE="${GITHUB_OUTPUT:-}"
BIN_DIR="${PIG_GOCACHEPROG_BIN_DIR:-}"
DISK_DIR="${PIG_GOCACHE_DISK_DIR:-}"
BUCKET="${PIG_GOCACHE_BUCKET:-}"
ENDPOINT="${PIG_GOCACHE_ENDPOINT:-}"
REGION="${PIG_GOCACHE_REGION:-${AWS_REGION:-}}"
PREFIX="${PIG_GOCACHE_PREFIX:-}"
ANONYMOUS="${PIG_GOCACHE_ANONYMOUS:-0}"
VERBOSE="${PIG_GOCACHE_VERBOSE:-0}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode) MODE="${2:?--mode requires a value}"; shift 2 ;;
    --mode=*) MODE="${1#*=}"; shift ;;
    --print-env) PRINT_ENV=1; shift ;;
    --github-env) GITHUB_ENV_FILE="${2:?--github-env requires a path}"; shift 2 ;;
    --github-output) GITHUB_OUTPUT_FILE="${2:?--github-output requires a path}"; shift 2 ;;
    --bin-dir) BIN_DIR="${2:?--bin-dir requires a path}"; shift 2 ;;
    --disk-dir) DISK_DIR="${2:?--disk-dir requires a path}"; shift 2 ;;
    --bucket) BUCKET="${2:-}"; shift 2 ;;
    --endpoint) ENDPOINT="${2:-}"; shift 2 ;;
    --region) REGION="${2:-}"; shift 2 ;;
    --prefix) PREFIX="${2:-}"; shift 2 ;;
    --anonymous) ANONYMOUS=1; shift ;;
    --verbose) VERBOSE=1; shift ;;
    -h|--help)
      sed -n '2,22p' "${BASH_SOURCE[0]}"
      exit 0
      ;;
    *)
      echo "setup-gocacheprog: unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

is_windows_bash() {
  case "${OSTYPE:-}" in
    msys*|cygwin*|win32*) return 0 ;;
  esac
  if [[ "${RUNNER_OS:-}" == "Windows" ]]; then
    return 0
  fi
  return 1
}

to_native_path() {
  local p="$1"
  if is_windows_bash && command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$p"
  else
    printf '%s' "$p"
  fi
}

emit_output() {
  local key="$1" val="$2"
  if [[ -n "$GITHUB_OUTPUT_FILE" ]]; then
    printf '%s=%s\n' "$key" "$val" >> "$GITHUB_OUTPUT_FILE"
  fi
}

emit_env() {
  local key="$1" val="$2"
  if [[ -n "$GITHUB_ENV_FILE" ]]; then
    printf '%s=%s\n' "$key" "$val" >> "$GITHUB_ENV_FILE"
  fi
  if [[ "$PRINT_ENV" == "1" ]]; then
    printf 'export %s=%q\n' "$key" "$val"
  fi
}

fallback_local() {
  local reason="$1"
  echo "[setup-gocacheprog] falling back to local Go disk cache ($reason)" >&2
  emit_output "enabled" "false"
  emit_output "mode" "local-fallback"
  emit_output "gocacheprog" ""
  exit 0
}

# Resolve effective mode with strict cache-poisoning guard.
resolve_mode() {
  local requested="$1"
  local event="${GITHUB_EVENT_NAME:-}"
  local ref="${GITHUB_REF:-}"
  case "$requested" in
    local|off|disabled)
      printf 'local'
      return 0
      ;;
    readonly)
      printf 'readonly'
      return 0
      ;;
    readwrite)
      # Never allow remote writes from pull_request or pull_request_target events even if
      # a caller mistakenly sets readwrite.
      if [[ "$event" == "pull_request" || "$event" == "pull_request_target" ]]; then
        echo "[setup-gocacheprog] downgrading readwrite -> readonly on $event to prevent cache poisoning" >&2
        printf 'readonly'
      else
        printf 'readwrite'
      fi
      return 0
      ;;
    auto)
      # Auto mode writes ONLY on trusted default-branch / release-branch / cache-warm pushes,
      # schedules, merge_group, or workflow_dispatch; PRs and local runs default to readonly.
      case "$event" in
        push|schedule|workflow_dispatch|merge_group)
          if [[ "$ref" == "refs/heads/main" || "$ref" == refs/heads/release/* ]]; then
            printf 'readwrite'
          else
            printf 'readonly'
          fi
          ;;
        *)
          printf 'readonly'
          ;;
      esac
      return 0
      ;;
    *)
      echo "[setup-gocacheprog] invalid mode '$requested' (want auto|readonly|readwrite|local)" >&2
      exit 2
      ;;
  esac
}

EFFECTIVE_MODE=$(resolve_mode "$MODE")
if [[ "$EFFECTIVE_MODE" == "local" ]]; then
  fallback_local "mode=local"
fi

if [[ -z "$BUCKET" ]]; then
  fallback_local "no bucket configured (PIG_GOCACHE_BUCKET unset)"
fi

ACCESS_KEY="${PIG_GOCACHE_ACCESS_KEY_ID:-${AWS_ACCESS_KEY_ID:-}}"
SECRET_KEY="${PIG_GOCACHE_SECRET_ACCESS_KEY:-${AWS_SECRET_ACCESS_KEY:-}}"
HAS_CREDS=0
if [[ -n "$ACCESS_KEY" && -n "$SECRET_KEY" ]]; then
  HAS_CREDS=1
fi

IS_ANON=0
case "$(printf '%s' "$ANONYMOUS" | tr '[:upper:]' '[:lower:]')" in
  1|true|yes) IS_ANON=1 ;;
esac

if [[ "$HAS_CREDS" == "0" && "$IS_ANON" == "0" ]]; then
  fallback_local "no S3/R2 credentials present (safe fork-PR fallback)"
fi

if [[ "$IS_ANON" == "1" ]]; then
  EFFECTIVE_MODE="readonly"
fi

if ! command -v go >/dev/null 2>&1; then
  fallback_local "go binary not found in PATH"
fi

# Choose OS-appropriate default paths for the helper binary and local backing disk cache.
if [[ -z "$BIN_DIR" ]]; then
  if [[ -n "${RUNNER_TEMP:-}" ]]; then
    BIN_DIR="$RUNNER_TEMP/pig-gocacheprog-bin"
  else
    BIN_DIR="${TMPDIR:-/tmp}/pig-gocacheprog-bin"
  fi
fi

if [[ -z "$DISK_DIR" ]]; then
  if [[ -n "${RUNNER_TEMP:-}" ]]; then
    DISK_DIR="$RUNNER_TEMP/pig-gocacheprog-disk"
  else
    go_cache=$(go env GOCACHE 2>/dev/null || true)
    if [[ -n "$go_cache" && "$go_cache" != "off" ]]; then
      if is_windows_bash && command -v cygpath >/dev/null 2>&1; then
        go_cache=$(cygpath -u "$go_cache")
      fi
      DISK_DIR="${go_cache}-prog"
    else
      DISK_DIR="${TMPDIR:-/tmp}/pig-gocacheprog-disk"
    fi
  fi
fi

mkdir -p "$BIN_DIR" "$DISK_DIR"

BIN_NAME="pig-gocacheprog"
if is_windows_bash; then
  BIN_NAME="pig-gocacheprog.exe"
fi
BIN_PATH="$BIN_DIR/$BIN_NAME"

# Build the helper with GOCACHEPROG unset so building the cache helper never recurses.
if ! (cd "$ROOT/automation/ci/gocacheprog" && GOWORK=off GOCACHEPROG="" CGO_ENABLED=0 go build -o "$BIN_PATH" .); then
  fallback_local "failed to build automation/ci/gocacheprog"
fi

# Default prefix isolates caches by OS/arch and Go version so cross-OS artifacts never collide.
if [[ -z "$PREFIX" ]]; then
  goos=$(go env GOOS)
  goarch=$(go env GOARCH)
  gover=$(go env GOVERSION)
  PREFIX="v1/${goos}-${goarch}/${gover}"
fi

NATIVE_BIN=$(to_native_path "$BIN_PATH")
NATIVE_DISK=$(to_native_path "$DISK_DIR")

# Verify the built helper can initialize its local disk store before exporting GOCACHEPROG.
SELF_CHECK_ARGS=(-self-check -disk-dir "$NATIVE_DISK" -bucket "$BUCKET" -prefix "$PREFIX")
[[ -z "$ENDPOINT" ]] || SELF_CHECK_ARGS+=(-endpoint "$ENDPOINT")
[[ -z "$REGION" ]] || SELF_CHECK_ARGS+=(-region "$REGION")
[[ "$EFFECTIVE_MODE" != "readonly" ]] || SELF_CHECK_ARGS+=(-readonly)
[[ "$IS_ANON" != "1" ]] || SELF_CHECK_ARGS+=(-anonymous)

if ! "$BIN_PATH" "${SELF_CHECK_ARGS[@]}" >/dev/null 2>&1; then
  fallback_local "helper self-check failed"
fi

# Pass all configuration via environment variables (PIG_GOCACHE_*) so GOCACHEPROG itself
# stays a single path without quoting issues on Windows paths containing spaces.
emit_env "PIG_GOCACHE_DISK_DIR" "$NATIVE_DISK"
emit_env "PIG_GOCACHE_BUCKET" "$BUCKET"
emit_env "PIG_GOCACHE_PREFIX" "$PREFIX"
[[ -z "$ENDPOINT" ]] || emit_env "PIG_GOCACHE_ENDPOINT" "$ENDPOINT"
[[ -z "$REGION" ]] || emit_env "PIG_GOCACHE_REGION" "$REGION"
if [[ "$EFFECTIVE_MODE" == "readonly" ]]; then
  emit_env "PIG_GOCACHE_READONLY" "1"
else
  emit_env "PIG_GOCACHE_READONLY" "0"
fi
if [[ "$IS_ANON" == "1" ]]; then
  emit_env "PIG_GOCACHE_ANONYMOUS" "1"
fi
if [[ "$VERBOSE" == "1" || "$VERBOSE" == "true" ]]; then
  emit_env "PIG_GOCACHE_VERBOSE" "1"
fi
emit_env "GOCACHEPROG" "$NATIVE_BIN"

emit_output "enabled" "true"
emit_output "mode" "remote-${EFFECTIVE_MODE}"
emit_output "gocacheprog" "$NATIVE_BIN"

echo "[setup-gocacheprog] enabled remote-${EFFECTIVE_MODE} cache (prefix=${PREFIX}, disk=${NATIVE_DISK})" >&2
