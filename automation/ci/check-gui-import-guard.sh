#!/usr/bin/env bash
# One-way dependency (docs/additive-features.md D614): plugin UI code may import chord; chord (and its tests)
# never imports a UI package. Direct imports are checked; GUI_PACKAGES is a space-separated override for the red test.
set -euo pipefail
cd "$(dirname "$0")/../.."
modpath=$(awk '$1 == "module" {print $2; exit}' go.mod)
gui=${GUI_PACKAGES:-"coding/uiview coding/presentation extensions/sdk/ui"}
status=0
while read -r pkg imports; do
  for imp in $imports; do
    for g in $gui; do
      case $imp in "$modpath/$g"|"$modpath/$g/"*) echo "chord imports GUI package: $pkg -> $imp" >&2; status=1 ;; esac
    done
  done
done < <(go list -test -f '{{.ImportPath}} {{join .Imports " "}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./chord/... 2>/dev/null)
[ $status -eq 0 ] && echo "gui import guard: chord imports no UI package"
exit $status
