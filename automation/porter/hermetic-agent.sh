#!/usr/bin/env bash
# Sourced by the Porter launchers. porter_hermetic_agent <dest> fills <dest> with the operator's credentials,
# model catalog and settings, minus every key that loads operator-owned code (packages, extensions, skills,
# prompts, themes), and trusts the project folder for this run only. The operator's settings are read, never written.
# porter_export_hermetic_agent (one launcher process) links auth.json and sessions/ to the operator's instead of copying
# them: a refresh that rotates an OAuth refresh token or a /login must reach the operator's credentials, and
# `make porter PORTER_ARGS=--continue` must find the earlier session after the private directory is removed.

porter_operator_agent_dir() {
  if [ -n "${PIG_CODING_AGENT_DIR:-}" ]; then
    printf '%s\n' "$PIG_CODING_AGENT_DIR"
  elif [ -n "${PI_CODING_AGENT_DIR:-}" ]; then
    printf '%s\n' "$PI_CODING_AGENT_DIR"
  else
    printf '%s\n' "${PIG_HOME:-$HOME/.pig}/agent"
  fi
}

porter_hermetic_agent() {
  local dest="$1" source name
  source="$(porter_operator_agent_dir)"
  mkdir -p "$dest"
  chmod 700 "$dest"
  for name in auth.json models.json; do
    if [ -f "$source/$name" ]; then
      cp "$source/$name" "$dest/$name"
      chmod 600 "$dest/$name"
    fi
  done
  python3 - "$source/settings.json" "$dest/settings.json" <<'PY'
import json, os, sys
source, dest = sys.argv[1:3]
settings = json.load(open(source)) if os.path.exists(source) else {}
for key in ("packages", "extensions", "skills", "prompts", "themes", "enabledModels"):
    settings.pop(key, None)
settings["defaultProjectTrust"] = "always"
with open(dest, "w") as out:
    json.dump(settings, out, indent=2)
PY
  chmod 600 "$dest/settings.json"
}

porter_export_hermetic_agent() {
  local dest="$1" source
  porter_hermetic_agent "$dest"
  source="$(porter_operator_agent_dir)"
  if [ -f "$source/auth.json" ]; then
    ln -sf "$source/auth.json" "$dest/auth.json"
  fi
  mkdir -p "$source/sessions"
  ln -s "$source/sessions" "$dest/sessions"
  PIG_HOME=$(dirname "$dest")
  export PIG_CODING_AGENT_DIR="$dest" PI_CODING_AGENT_DIR="$dest" PIG_HOME
}
