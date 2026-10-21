#!/usr/bin/env bash
# Runs a parity probe's Go program from the binary the runner built before the timed run.
#
#   prebuilt-go-run.sh ./PACKAGE [ARGS...]
#
# Use it in place of `go run ./PACKAGE ARGS` in a cli-mode script (shell, Node or Python) and list the PACKAGE in the scenario's [env]
# pig_go_programs or pi_go_programs. The runner builds those programs before the first timed run and names their directory in
# PIG_PARITY_GO_PROGRAMS (test/parity/runner/go_prebuild.go), so the run's runtime and timeout cover the probe, not the Go toolchain.
# The program runs in the current directory, as `go run` runs it, and its exit status is this script's.
set -euo pipefail
die() {
	printf 'prebuilt-go-run.sh: %s\n' "$*" >&2
	exit 2
}
[[ -n ${PIG_PARITY_GO_PROGRAMS:-} ]] || die "PIG_PARITY_GO_PROGRAMS is not set: the parity runner sets it for a scenario that lists its packages in [env] pig_go_programs or pi_go_programs"
(($#)) || die "no package to run"
package=$1
shift
[[ $package == ./* ]] || die "the package must be ./-relative: $package"
rel=${package#./}
rel=${rel%/}
binary=$PIG_PARITY_GO_PROGRAMS/$rel/${rel##*/}
if [[ ! -x $binary && -x $binary.exe ]]; then
	binary=$binary.exe
fi
[[ -x $binary ]] || die "$package has no prebuilt program: list it in the scenario's [env] pig_go_programs or pi_go_programs"
exec "$binary" "$@"
