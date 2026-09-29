#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=automation/layout/arguments.sh
source "$here/arguments.sh"
for section in go build docs; do
    if [[ ! -f "$here/$section.sh" ]]; then
        echo "layout: missing section $here/$section.sh; merge all layout sections before applying" >&2
        exit 1
    fi
done
parity=parity
state=original
if ((${#options[@]})); then
    state=$(python3 -B "$here/release.py" --state "$root")
    parity=test/parity
fi
if [[ "$state" == original ]]; then
    for section in go build docs; do
        bash "$here/$section.sh" "${options[@]}" "$root"
    done
fi
if ((${#options[@]})); then
    python3 -B "$here/release.py" "$root"
fi
cd -- "$root"
go run "./$parity/cmd/gointerfaces" -out "$parity/interfaces/pig-go.json"
version=$(python3 - <<'PY'
from pathlib import Path
import re
source = Path('internal/coding/pigversion/pigversion.go').read_text()
match = re.search(r'^const UpstreamVersion = "([^"]+)"$', source, re.MULTILINE)
if match is None:
    raise SystemExit('layout: cannot read the current UpstreamVersion pin')
print(match[1])
PY
)
go run "./$parity/cmd/interfacerecommend" \
    -inventory "$parity/interfaces/upstream-v$version.json" \
    -observable "$parity/interfaces/cli-v$version.json" \
    -go-inventory "$parity/interfaces/pig-go.json" \
    -out "$parity/interfaces/recommendations-v$version.json"
make coverage RESULTS=
make custom-factory-ledger
make normalization-inventory
