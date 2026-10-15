#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Nightly fuzzing: every Fuzz target in the module runs for PIG_FUZZ_TIME (default 30s) with PIG_FUZZ_PARALLEL workers
# (default 4). go test -fuzz accepts one package and one target per run, so the script enumerates them. A package whose
# targets cannot be listed fails the run instead of dropping out of it. A failure leaves its input under the package's
# testdata/fuzz directory; the workflow uploads it.
set -euo pipefail

fuzztime="${PIG_FUZZ_TIME:-30s}"
parallel="${PIG_FUZZ_PARALLEL:-4}"
mapfile -t packages < <(git grep -l -E '^func Fuzz[A-Za-z0-9_]+\([A-Za-z_][A-Za-z0-9_]* \*testing\.F\)' -- '*_test.go' | xargs -n1 dirname | sort -u)
if [ "${#packages[@]}" -eq 0 ]; then
	echo "fuzz-nightly: no Fuzz targets found" >&2
	exit 1
fi

ran=0
failed=()
for dir in "${packages[@]}"; do
	if ! listed="$(go test -list '^Fuzz' "./$dir")"; then
		failed+=("$dir:go test -list")
		continue
	fi
	mapfile -t targets < <(grep -E '^Fuzz' <<<"$listed" || true)
	for target in "${targets[@]}"; do
		echo "== $dir $target ($fuzztime)"
		ran=$((ran + 1))
		if ! go test -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime" -parallel "$parallel" "./$dir"; then
			failed+=("$dir:$target")
		fi
	done
done

if [ "${#failed[@]}" -ne 0 ]; then
	printf 'fuzz-nightly: failing target: %s\n' "${failed[@]}" >&2
	exit 1
fi
echo "fuzz-nightly: $ran targets ran $fuzztime each without a failure."
