#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# build-pig-acp.sh PIG_ARCHIVE OUT_DIR WORK_DIR
#
# Builds the pig-acp-<version>-<goos>-<goarch> release archive for one target.
# VERSION (PiG's version, no v prefix), GOOS, GOARCH and FORMAT (tar|zip) come
# from the environment, as in release-candidate.yml.
#
# The archive is flat: pig-acp, pig and the notices sit at its root, with no
# version in any path, so the ACP registry's fixed `cmd` (./pig-acp) stays valid
# across releases. The archive carries pig-acp and the pig executable side by
# side. The pig executable is extracted from PIG_ARCHIVE, the release
# candidate's own pig-<version>-<goos>-<goarch> archive, so the pig bytes are the
# pig bytes that archive's tests and attestation cover; this script never
# rebuilds pig. pig-acp is built from the Pigpen Package pinned in
# automation/release/pig-acp.lock (the source `pig acp install` builds), verified
# by size and sha512 before it is extracted, with CGO disabled.
#
# Writes OUT_DIR/<name>.{tar.gz,zip} and the staged tree WORK_DIR/<name>/ (what
# the SBOM describes). Prints "artifact=<OUT_DIR>/<archive>" and "stage=<dir>"
# for $GITHUB_OUTPUT.
set -euo pipefail

archive=${1:?usage: build-pig-acp.sh PIG_ARCHIVE OUT_DIR WORK_DIR}
out=${2:?usage: build-pig-acp.sh PIG_ARCHIVE OUT_DIR WORK_DIR}
work=${3:?usage: build-pig-acp.sh PIG_ARCHIVE OUT_DIR WORK_DIR}
: "${VERSION:?}" "${GOOS:?}" "${GOARCH:?}" "${FORMAT:?}"

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
name="pig-acp-${VERSION}-${GOOS}-${GOARCH}"
pig_name="pig-${VERSION}-${GOOS}-${GOARCH}"
pig_bin=pig
acp_bin=pig-acp
if [ "$GOOS" = windows ]; then pig_bin=pig.exe; acp_bin=pig-acp.exe; fi

rm -rf "${work:?}/pig" "${work:?}/src" "${work:?}/$name"
mkdir -p "$out" "$work/pig" "$work/src"
work=$(cd "$work" && pwd)
out_abs=$(cd "$out" && pwd)
archive_abs=$(cd "$(dirname "$archive")" && pwd)/$(basename "$archive")

# The pig archive's own contents: the executable and the notices it ships.
if [ "$FORMAT" = zip ]; then
  unzip -q "$archive_abs" -d "$work/pig"
else
  tar -xzf "$archive_abs" -C "$work/pig"
fi
pig_root="$work/pig/$pig_name"
for member in "$pig_bin" LICENSE NOTICE THIRD_PARTY_NOTICES.md LICENSES; do
  [ -e "$pig_root/$member" ] || { echo "build-pig-acp: $archive has no $pig_name/$member" >&2; exit 1; }
done

source_dir=$(python3 "$here/pig-acp-source.py" --lock "$here/pig-acp.lock" "$work/src")
acp_version=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$here/pig-acp.lock")

stage="$work/$name"
mkdir -p "$stage/PIG-ACP"
(
  cd "$source_dir"
  GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=$acp_version" -o "$stage/$acp_bin" .
)
cp "$pig_root/$pig_bin" "$stage/$pig_bin"
cp "$pig_root/LICENSE" "$pig_root/NOTICE" "$pig_root/THIRD_PARTY_NOTICES.md" "$stage/"
cp -R "$pig_root/LICENSES" "$stage/"
# pig-acp's own license and the attribution for the pi-acp port it derives from.
cp "$work/src/LICENSE" "$stage/PIG-ACP/LICENSE"
cp "$work/src/CREDITS.md" "$stage/PIG-ACP/CREDITS.md"
cp "$work/src/port/oracle/pi-acp/LICENSE" "$stage/PIG-ACP/pi-acp-LICENSE"
chmod 0755 "$stage/$pig_bin" "$stage/$acp_bin"

if [ "$FORMAT" = zip ]; then
  (cd "$stage" && zip -qr "$out_abs/$name.zip" .)
  artifact="$out/$name.zip"
else
  (cd "$stage" && tar -czf "$out_abs/$name.tar.gz" .)
  artifact="$out/$name.tar.gz"
fi
printf 'artifact=%s\n' "$artifact"
printf 'stage=%s\n' "$stage"
