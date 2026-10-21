#!/usr/bin/env bash
# Runs a parity probe's Go tests from the test binaries the runner built before the timed run.
#
#   prebuilt-go-test.sh ./PACKAGE... [-run REGEXP] [-count N] [-v]
#   prebuilt-go-test.sh --binary ./PACKAGE
#
# Use it in place of `go test` in a cli-mode pig script and list every PACKAGE in the scenario's [env] pig_go_tests.
# The runner builds those test binaries before the first timed run and names their directory in PIG_PARITY_GO_TESTS
# (test/parity/runner/go_prebuild.go), so the run's runtime and timeout cover the probe, not the Go toolchain.
# As go test does, each binary runs in its package directory with -test.paniconexit0 and a 10-minute -test.timeout,
# and its stdout and stderr both go to stdout. An `ok` or `FAIL` line follows each package, and the exit status is 1
# when a package fails. Packages run one after another.
set -euo pipefail
die() {
	printf 'prebuilt-go-test.sh: %s\n' "$*" >&2
	exit 2
}
[[ -n ${PIG_PARITY_GO_TESTS:-} ]] || die "PIG_PARITY_GO_TESTS is not set: the parity runner sets it for a scenario that lists its packages in [env] pig_go_tests"
flags=(-test.paniconexit0 -test.timeout=10m0s)
packages=()
print_binary=0
while (($#)); do
	case $1 in
	--binary)
		# Print the prebuilt test binary of the one PACKAGE, for a probe that must start it itself (a process group it signals).
		print_binary=1
		shift
		;;
	-run | -count)
		(($# >= 2)) || die "$1 needs a value"
		flags+=("-test.${1#-}=$2")
		shift 2
		;;
	-run=* | -count=*)
		flags+=("-test.${1#-}")
		shift
		;;
	-v)
		flags+=(-test.v=true)
		shift
		;;
	./*)
		packages+=("$1")
		shift
		;;
	*) die "unsupported argument: $1" ;;
	esac
done
((${#packages[@]})) || die "no package to test"
if ((print_binary)); then
	((${#packages[@]} == 1)) || die "--binary takes one package"
	rel=${packages[0]#./}
	rel=${rel%/}
	binary=$PIG_PARITY_GO_TESTS/$rel/${rel##*/}.test
	if [[ ! -x $binary && -x $binary.exe ]]; then
		binary=$binary.exe
	fi
	[[ -x $binary ]] || die "${packages[0]} has no prebuilt test binary: list it in the scenario's [env] pig_go_tests"
	printf '%s\n' "$binary"
	exit 0
fi
status=0
for package in "${packages[@]}"; do
	rel=${package#./}
	rel=${rel%/}
	binary=$PIG_PARITY_GO_TESTS/$rel/${rel##*/}.test
	if [[ ! -x $binary && -x $binary.exe ]]; then
		binary=$binary.exe
	fi
	[[ -x $binary ]] || die "$package has no prebuilt test binary: list it in the scenario's [env] pig_go_tests"
	if (cd -- "$package" && "$binary" "${flags[@]}" 2>&1); then
		printf 'ok  \t%s\n' "$package"
	else
		printf 'FAIL\t%s\n' "$package"
		status=1
	fi
done
exit "$status"
