#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# sign-update-manifest.sh MANIFEST SIGNING_KEYS TRUST_ROOTS
#
# Signs the self-update manifest MANIFEST with every Ed25519 private key in
# the PEM file SIGNING_KEYS and prints the signatures as one line of
# comma-separated base64, the update.json.sig format pig reads
# (internal/codingagent verifyReleaseSignature accepts the manifest when any
# signature verifies against any key it trusts).
#
# SIGNING_KEYS holds one key normally and two during a key rotation (outgoing
# and incoming, see docs/project/RELEASING.md), so binaries that trust only
# the outgoing key and binaries that trust only the incoming one both verify.
#
# Each signature must verify against at least one public key in the PEM file
# TRUST_ROOTS (automation/release/update-trust.pem); every key in that file is
# tried, because openssl reads only the first key of a multi-key PEM file. A
# signing key whose public key is not trusted fails the run.
set -euo pipefail

usage="usage: sign-update-manifest.sh MANIFEST SIGNING_KEYS TRUST_ROOTS"
manifest=${1:?$usage}
signing_keys=${2:?$usage}
trust_roots=${3:?$usage}
for file in "$manifest" "$signing_keys" "$trust_roots"; do
	[[ -f $file ]] || { echo "sign-update-manifest.sh: $file not found" >&2; exit 2; }
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
chmod 700 "$work"

# split_pem FILE PREFIX writes each PEM block of FILE to PREFIX.<n>.pem and
# prints how many it wrote.
split_pem() {
	awk -v prefix="$2" '
		/^-----BEGIN / { n++; out = prefix "." n ".pem"; inside = 1 }
		inside { print > out }
		/^-----END / { inside = 0; close(out) }
		END { print n + 0 }
	' "$1"
}

key_count=$(split_pem "$signing_keys" "$work/key")
trust_count=$(split_pem "$trust_roots" "$work/trust")
((key_count > 0)) || { echo "sign-update-manifest.sh: $signing_keys holds no PEM private key" >&2; exit 1; }
((trust_count > 0)) || { echo "sign-update-manifest.sh: $trust_roots holds no PEM public key" >&2; exit 1; }

signatures=()
for ((k = 1; k <= key_count; k++)); do
	sig="$work/sig.$k"
	openssl pkeyutl -sign -rawin -inkey "$work/key.$k.pem" -in "$manifest" -out "$sig"
	trusted=
	for ((t = 1; t <= trust_count; t++)); do
		if openssl pkeyutl -verify -rawin -pubin -inkey "$work/trust.$t.pem" \
			-in "$manifest" -sigfile "$sig" >/dev/null 2>&1; then
			trusted=1
			break
		fi
	done
	[[ -n $trusted ]] || {
		echo "sign-update-manifest.sh: signing key $k of $key_count has no public key in $trust_roots" >&2
		exit 1
	}
	signatures+=("$(openssl base64 -A -in "$sig")")
done

(IFS=,; printf '%s\n' "${signatures[*]}")
