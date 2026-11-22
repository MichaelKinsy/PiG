#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Nightly fuzzing: every Fuzz target in the module runs for PIG_FUZZ_TIME (default 30s) with PIG_FUZZ_PARALLEL workers
# (default 4). go test -fuzz accepts one package and one target per run, so the script enumerates them. Each fuzz file's
# //go:build line is read so build-tagged targets are listed and run with their tag; unsupported build expressions fail
# loudly. A package whose targets cannot be listed fails the run instead of dropping out of it. A failure leaves its
# input under the package's testdata/fuzz directory; the workflow uploads it.
set -euo pipefail

list_only=0
if [ "${1:-}" = "--list" ]; then
	list_only=1
	shift
fi
if [ "$#" -ne 0 ]; then
	echo "fuzz-nightly: usage: $0 [--list]" >&2
	exit 2
fi

fuzztime="${PIG_FUZZ_TIME:-30s}"
parallel="${PIG_FUZZ_PARALLEL:-4}"
script_dir="$(cd "$(dirname "$0")" && pwd)"
skip_file="${PIG_FUZZ_SKIP_FILE:-$script_dir/fuzz-nightly-skip.txt}"

declare -A skip_reasons=()
if [ -f "$skip_file" ]; then
	line_no=0
	while IFS= read -r raw_line || [ -n "$raw_line" ]; do
		line_no=$((line_no + 1))
		line="${raw_line#"${raw_line%%[![:space:]]*}"}"
		if [ -z "$line" ] || [[ "$line" == \#* ]]; then
			continue
		fi
		entry="${line%%[[:space:]]*}"
		rest="${line#"$entry"}"
		reason="${rest#"${rest%%[![:space:]]*}"}"
		if [[ "$entry" != *:* ]] || [ -z "$reason" ]; then
			echo "fuzz-nightly: $skip_file:$line_no: invalid skip entry '$entry' (want '<package>:<target> <reason>')" >&2
			exit 1
		fi
		skip_reasons["$entry"]="$reason"
	done <"$skip_file"
fi

mapfile -t fuzz_files < <(git grep -l -E '^func Fuzz[A-Za-z0-9_]+\([A-Za-z_][A-Za-z0-9_]* \*testing\.F\)' -- '*_test.go' | sort -u)
if [ "${#fuzz_files[@]}" -eq 0 ]; then
	echo "fuzz-nightly: no Fuzz targets found" >&2
	exit 1
fi

extract_build_tag() {
	local file="$1"
	local line expr=""
	while IFS= read -r line || [ -n "$line" ]; do
		if [[ "$line" =~ ^package[[:space:]] ]]; then
			break
		fi
		if [[ "$line" =~ ^//go:build[[:space:]]+(.+)$ ]]; then
			if [ -n "$expr" ]; then
				echo "fuzz-nightly: $file: multiple //go:build lines are not supported" >&2
				return 1
			fi
			expr="${BASH_REMATCH[1]}"
			# Trim leading and trailing whitespace.
			expr="${expr#"${expr%%[![:space:]]*}"}"
			expr="${expr%"${expr##*[![:space:]]}"}"
		fi
	done <"$file"
	if [ -z "$expr" ]; then
		printf '\n'
		return 0
	fi
	if [[ "$expr" =~ ^[A-Za-z0-9_]+$ ]]; then
		printf '%s\n' "$expr"
		return 0
	fi
	echo "fuzz-nightly: $file: unsupported build constraint '//go:build $expr'" >&2
	return 1
}

declare -A seen_groups=()
groups=()
for file in "${fuzz_files[@]}"; do
	dir="$(dirname "$file")"
	tag="$(extract_build_tag "$file")"
	key="${dir}|${tag}"
	if [ -z "${seen_groups[$key]+x}" ]; then
		seen_groups["$key"]=1
		groups+=("$key")
	fi
done

mapfile -t sorted_groups < <(printf '%s\n' "${groups[@]}" | sort)

ran=0
listed_count=0
skipped=0
failed=()
declare -A seen_targets=()

for key in "${sorted_groups[@]}"; do
	dir="${key%%|*}"
	tag="${key#*|}"
	tag_args=()
	if [ -n "$tag" ]; then
		tag_args=(-tags "$tag")
	fi
	if ! listed="$(go test "${tag_args[@]}" -list '^Fuzz' "./$dir")"; then
		if [ -n "$tag" ]; then
			failed+=("$dir($tag):go test -list")
		else
			failed+=("$dir:go test -list")
		fi
		continue
	fi
	mapfile -t targets < <(grep -E '^Fuzz' <<<"$listed" || true)
	for target in "${targets[@]}"; do
		target_id="$dir:$target"
		if [ -n "${seen_targets[$target_id]+x}" ]; then
			continue
		fi
		seen_targets["$target_id"]=1
		if [ -n "${skip_reasons[$target_id]+x}" ]; then
			echo "fuzz-nightly: skipped $target_id: ${skip_reasons[$target_id]}"
			skipped=$((skipped + 1))
			continue
		fi
		if [ "$list_only" -eq 1 ]; then
			printf '%s\t%s\t%s\n' "$dir" "$target" "${tag:--}"
			listed_count=$((listed_count + 1))
			continue
		fi
		echo "== $dir $target ($fuzztime)"
		ran=$((ran + 1))
		if ! go test "${tag_args[@]}" -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime" -parallel "$parallel" "./$dir"; then
			failed+=("$target_id")
		fi
	done
done

if [ "$list_only" -eq 1 ]; then
	echo "fuzz-nightly: listed $listed_count targets, skipped $skipped (named above), failed ${#failed[@]}"
else
	echo "fuzz-nightly: ran $ran targets, skipped $skipped (named above), failed ${#failed[@]}"
fi

if [ "${#failed[@]}" -ne 0 ]; then
	printf 'fuzz-nightly: failing target: %s\n' "${failed[@]}" >&2
	exit 1
fi
