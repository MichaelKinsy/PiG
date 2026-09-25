#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# Prepare a PiG development environment, or report what is missing.
#
#   make setup    runs this script
#   make doctor   runs it with --check, which reports and changes nothing
#
# Options:
#   --check            report every component and exit; install nothing
#   --background       run detached; progress goes to $PIG_DEV_HOME/setup.log
#   --no-build         skip the final build of bin/pig
#   --node             npm ci in extensions/sdk-ts (Pi mirror, parity, SDK tests)
#   --harnesses=LIST   install eval harnesses from evals/harnesses.toml
#                      (comma-separated names, or all)
#   --all              --node --harnesses=all
#
# Everything installed goes under $PIG_DEV_HOME (default ${XDG_CACHE_HOME:-~/.cache}/pig-dev) or
# the checkout's ignored directories. The script never uses sudo. Make includes
# $PIG_DEV_HOME/env.mk, so every make target uses the toolchain and module proxy
# chosen here. Shells get the same values from `source $PIG_DEV_HOME/env.sh`.
#
# When proxy.golang.org is unreachable and github.com is reachable, the script
# starts automation/dev/goproxy-github.py, a local module proxy that serves only
# content matching the checked-in go.sum files.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
dev_home="${PIG_DEV_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}/pig-dev}"
port="${PIG_GOPROXY_PORT:-8765}"
check=0 background=0 build=1 node=0 harnesses=""
for arg in "$@"; do
	case "$arg" in
	--check) check=1 ;;
	--background) background=1 ;;
	--no-build) build=0 ;;
	--node) node=1 ;;
	--harnesses=*) harnesses="${arg#--harnesses=}" ;;
	--all) node=1 harnesses=all ;;
	-h | --help)
		sed -n '4,/^set -euo pipefail/p' "${BASH_SOURCE[0]}" | sed -e '$d' -e 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "setup: unknown option: $arg (try --help)" >&2
		exit 2
		;;
	esac
done
mkdir -p "$dev_home"

have() { command -v "$1" >/dev/null 2>&1; }

if ((background)); then
	rest=()
	for arg in "$@"; do [[ $arg == --background ]] || rest+=("$arg"); done
	detach=(nohup)
	have setsid && detach=(setsid nohup) # A plain background job dies with the caller's session in some sandboxes.
	"${detach[@]}" "${BASH_SOURCE[0]}" ${rest[@]+"${rest[@]}"} >"$dev_home/setup.log" 2>&1 </dev/null &
	echo "$!" >"$dev_home/setup.pid"
	echo "setup: running in the background as pid $!"
	echo "setup: follow it with: tail -f $dev_home/setup.log (it ends with 'setup: done' or 'setup: FAILED')"
	exit 0
fi

summary=() missing=0
row() {
	summary+=("$(printf '  %-10s %-8s %s' "$1" "$2" "$3")")
	printf 'setup: %-10s %-8s %s\n' "$1" "$2" "$3"
}
need() {
	row "$1" missing "$2"
	missing=1
}
sha256() { if have sha256sum; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
reachable() { curl -fsS -m 8 -o /dev/null "$1" 2>/dev/null; }

# Another installer (mise, asdf, a shell profile) may export GOROOT or GOBIN
# for a different Go. A foreign GOROOT makes the pinned go report "package
# bufio is not in std", so setup ignores both and env.mk/env.sh clear them.
leaked=()
[[ -n ${GOROOT:-} ]] && leaked+=("GOROOT=$GOROOT")
[[ -n ${GOBIN:-} ]] && leaked+=("GOBIN=$GOBIN")
unset GOROOT GOBIN

# Go toolchain: the version named by go.mod's toolchain directive.
go_want="$(awk '$1 == "toolchain" { print $2; exit }' "$root/go.mod")"
[[ -n $go_want ]] || go_want="go$(awk '$1 == "go" { print $2; exit }' "$root/go.mod")"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$(uname -m)" in x86_64 | amd64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) arch="$(uname -m)" ;; esac
pinned="$dev_home/toolchains/$go_want"
go_bin="" go_mode=""
goversion() { GOTOOLCHAIN=local "$1" env GOVERSION 2>/dev/null || true; }
find_go() {
	if [[ -x $pinned/bin/go && "$(goversion "$pinned/bin/go")" == "$go_want" ]]; then
		go_bin="$pinned/bin/go" go_mode=pinned
	elif have go && [[ "$(goversion go)" == "$go_want" ]]; then
		go_bin="$(command -v go)" go_mode=path
	elif ((!check)) && have go && [[ "$(cd "$root" && GOTOOLCHAIN=auto go env GOVERSION 2>/dev/null)" == "$go_want" ]]; then
		go_bin="$(command -v go)" go_mode=auto # go downloads go.mod's toolchain itself.
	fi
}
install_go() { # Called in a condition, so errexit is off here: every step checks its own status.
	local tmp url="" sum="" file="$go_want.$os-$arch.tar.gz" index top
	tmp="$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX")"
	if index="$(curl -fsSL -m 20 'https://go.dev/dl/?mode=json&include=all' 2>/dev/null)"; then
		sum="$(python3 -c 'import json, sys
print(next((f["sha256"] for r in json.load(sys.stdin) for f in r["files"] if f["filename"] == sys.argv[1]), ""))' "$file" <<<"$index")"
		[[ -n $sum ]] && url="https://go.dev/dl/$file"
	fi
	if [[ -z $url ]]; then
		read -r url sum < <(awk -v v="${go_want#go}" -v o="$os" -v a="$arch" \
			'!/^#/ && $1 == "go" && $2 == v && $3 == o && $4 == a { print $6, $7; exit }' "$root/automation/dev/toolchains.lock") || true
	fi
	if [[ -z $url || -z $sum ]]; then
		echo "setup: no verified download of $go_want for $os/$arch; install it from https://go.dev/dl/" >&2
		rm -rf "$tmp"
		return 1
	fi
	echo "setup: downloading $url"
	curl -fsSL -m 900 -o "$tmp/go.tar.gz" "$url" || { rm -rf "$tmp"; return 1; }
	[[ "$(sha256 "$tmp/go.tar.gz")" == "$sum" ]] || { echo "setup: checksum mismatch for $url" >&2; rm -rf "$tmp"; return 1; }
	if ! { mkdir -p "$tmp/x" && tar -xzf "$tmp/go.tar.gz" -C "$tmp/x"; }; then
		rm -rf "$tmp"
		return 1
	fi
	top="$tmp/x"
	[[ -x $top/go/bin/go ]] && top="$top/go" # go.dev archives nest under go/; actions/go-versions archives do not.
	rm -rf "$pinned" && mkdir -p "$(dirname "$pinned")" && mv "$top" "$pinned"
	rm -rf "$tmp"
}
find_go
if [[ -z $go_bin ]] && ((!check)) && install_go; then find_go; fi
if ((${#leaked[@]})); then
	row goenv warn "ignored ${leaked[*]} from another installer; make and env.sh unset them"
fi
if [[ -n $go_bin ]]; then
	row go ok "$go_want via $go_mode ($go_bin)"
else
	need go "$go_want not available; run make setup, or install it from https://go.dev/dl/"
fi

# Module downloads: the configured GOPROXY, else a verified local proxy over GitHub.
proxy_mode=""
if [[ -n $go_bin ]]; then
	# Read the user's own setting, not the fallback an earlier run exported
	# through env.mk (make passes it to this script).
	if [[ ${PIG_GOPROXY_FALLBACK:-} == 1 ]]; then
		unset GOPROXY GOSUMDB GOWORK PIG_GOPROXY_FALLBACK
	fi
	configured="$(cd "$root" && { GOTOOLCHAIN=local "$go_bin" env GOPROXY 2>/dev/null || true; })"
	# go env prints nothing when Go's config directory is unreadable, as in
	# some sandboxes; Go then uses its default, so check that.
	[[ -n $configured ]] || configured="https://proxy.golang.org,direct"
	first="${configured%%[,|]*}"
	if [[ $first == http* ]] && reachable "$first/golang.org/x/mod/@v/list"; then
		proxy_mode=configured
		row modules ok "GOPROXY=$configured"
		# curl and Go verify TLS differently. Behind an intercepting proxy, or
		# in a macOS sandbox without the trust daemon, only Go fails.
		tls_probe="$(cd "$root" && { GOTOOLCHAIN=local GOFLAGS='' GOPROXY="$configured" "$go_bin" list -m -versions golang.org/x/mod 2>&1 || true; })"
		if [[ $tls_probe == *x509* || $tls_probe == *certificate* ]]; then
			need tls "Go cannot verify $first ($(grep -m1 -o 'x509:.*' <<<"$tls_probe")). Export SSL_CERT_FILE to your CA bundle (on macOS: export SSL_CERT_FILE=/etc/ssl/cert.pem) and rerun"
		fi
	elif reachable "https://github.com/"; then
		proxy_mode=github
		if ((check)); then
			if reachable "http://127.0.0.1:$port/probe/@v/v0.0.0.info"; then
				row modules ok "local verified proxy on 127.0.0.1:$port ($first unreachable)"
			else
				need modules "$first unreachable; make setup starts a verified local proxy"
			fi
		elif python3 "$root/automation/dev/goproxy-github.py" --ensure --port "$port" --root "$root"; then
			row modules ok "local verified proxy on 127.0.0.1:$port ($first unreachable)"
		else
			need modules "automation/dev/goproxy-github.py did not start; see $dev_home/goproxy-github-$port.out"
		fi
	else
		proxy_mode=offline
		row modules offline "no module proxy and no GitHub; builds use the module cache only"
	fi
fi

if ((!check)) && [[ -n $go_bin ]]; then
	vars=()
	[[ $go_mode == pinned ]] && vars+=("PATH=$pinned/bin:\$PATH" "GOTOOLCHAIN=local")
	[[ $proxy_mode == github ]] && vars+=("GOPROXY=http://127.0.0.1:$port" "GOSUMDB=off" "PIG_GOPROXY_FALLBACK=1")
	vars+=("GOFLAGS=-modcacherw")
	{
		echo "# Written by automation/dev/setup.sh. Rerun it to change these values."
		echo "unset GOROOT GOBIN"
		for v in "${vars[@]}"; do echo "export ${v%%=*}=\"${v#*=}\""; done
	} >"$dev_home/env.sh"
	{
		echo "# Written by automation/dev/setup.sh. Rerun it to change these values."
		echo "# A GOROOT or GOBIN exported by another installer overrides the pinned Go."
		echo "unexport GOROOT GOBIN"
		for v in "${vars[@]}"; do
			value="${v#*=}"
			echo "export ${v%%=*} := ${value//\$PATH/\$(PATH)}"
		done
		if [[ $proxy_mode == github ]]; then
			echo "# Restart the verified module proxy if it stopped (sandbox restarts end background processes)."
			echo "PIG_GOPROXY_READY := \$(shell python3 $root/automation/dev/goproxy-github.py --ensure --port $port --root $root >/dev/null 2>&1 && echo yes)"
		fi
	} >"$dev_home/env.mk"
	row env ok "$dev_home/env.mk (make) and env.sh (shells)"
fi

# Node is needed for the TypeScript SDK, Pi parity, and eval harnesses.
node_needed=$node
[[ -n $harnesses ]] && node_needed=1
if have node && node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>22||(a===22&&b>=13)?0:1)'; then
	row node ok "$(node --version)"
elif ((node_needed)); then
	need node "Node >= 22.13 is required for --node and --harnesses (https://nodejs.org/)"
else
	row node absent "optional: needed for parity and evals"
fi

if ((node)); then
	if [[ -d $root/extensions/sdk-ts/node_modules/@earendil-works ]]; then
		row sdk-ts ok "extensions/sdk-ts/node_modules"
	elif ((check)); then
		need sdk-ts "run: make setup SETUP_ARGS=--node"
	elif (cd "$root/extensions/sdk-ts" && npm ci --no-audit --no-fund); then
		row sdk-ts ok "npm ci"
	else
		need sdk-ts "npm ci failed in extensions/sdk-ts"
	fi
fi

if [[ -n $harnesses ]]; then
	mode=()
	((check)) && mode=(--check)
	if PYTHONPATH="$root/evals" python3 -m pigeval install --harnesses "$harnesses" --prefix "$dev_home/harnesses" ${mode[@]+"${mode[@]}"}; then
		row harnesses ok "$harnesses under $dev_home/harnesses"
	else
		need harnesses "see the lines above; rerun: make setup SETUP_ARGS=--harnesses=$harnesses"
	fi
fi

if ((build && !check)) && [[ -n $go_bin ]]; then
	start=$SECONDS
	# shellcheck source=/dev/null # env.sh is written above for this machine.
	if (set -a && source "$dev_home/env.sh" && set +a && cd "$root" && "$go_bin" build -buildvcs=false -trimpath -o bin/pig ./cmd/pig); then
		row build ok "bin/pig in $((SECONDS - start)) s ($("$root/bin/pig" --version 2>/dev/null | head -1))"
	else
		need build "go build ./cmd/pig failed; the error is above"
	fi
fi

echo
echo "PiG development environment ($dev_home)"
printf '%s\n' "${summary[@]}"
if ((missing)); then
	echo "setup: FAILED"
	exit 1
fi
echo "setup: done. Next: make help"
