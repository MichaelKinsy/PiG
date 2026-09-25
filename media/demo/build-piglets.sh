#!/usr/bin/env bash
# Native Linux builds. The selected Go and Rust resources keep their normal realization.
set -euo pipefail
: "${DEMO_ROOT:?Run stage.sh, then source the generated env.sh}"
cd "$HOME/piglet"
for name in pig-go pig-demo; do
    printf '\n$ pig piglet build %s.yaml --format binary --out ../dist/%s\n' "$name" "$name"
    start=$SECONDS
    if ! pig piglet build "$name.yaml" --format binary --out "../dist/$name" 2>&1 | tee "$DEMO_ROOT/logs/build-$name.log"; then
        printf 'Build failed. Inspect logs/build-%s.log before recording.\n' "$name" >&2
        exit 1
    fi
    printf 'Built %s on Linux in %ss\n' "$name" "$((SECONDS - start))"
    (cd ../dist && sha256sum "$name")
done
