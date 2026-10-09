# SPDX-License-Identifier: MIT
# Sourced by the layout entry points after they set here.
# shellcheck shell=bash
# shellcheck disable=SC2154,SC2034 # here comes from the sourcing script; options and root are its outputs.
options=()
root=
for argument in "$@"; do
    case "$argument" in
        --keep-public-libraries) options=(--keep-public-libraries) ;;
        -h|--help) echo "usage: $0 [--keep-public-libraries] [repo-root]"; exit 0 ;;
        -*) echo "layout: unknown option: $argument" >&2; exit 2 ;;
        *)
            if [[ -n "$root" ]]; then echo "usage: $0 [--keep-public-libraries] [repo-root]" >&2; exit 2; fi
            root=$argument ;;
    esac
done
root=${root:-$(git -C "$here" rev-parse --show-toplevel)}
