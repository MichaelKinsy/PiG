#!/bin/bash
# The long-session measurement, native against web, one server and one session: usage: measure.sh <fixture data dir> <exchanges> <out dir>
# (needs P2_UI_NODE_PATH; the fixture is made by the first run through Chat.send, 5000 exchanges = 10k messages).
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
DATA=$1; N=$2; OUT=$3; mkdir -p "$OUT"
T=$(mktemp -d)
# NATIVE_TEST=<prebuilt test binary> skips the build (a host whose Go and Chrome live in different containers).
NATIVE=${NATIVE_TEST:-/tmp/pd2-native.test}
[ -n "${NATIVE_TEST:-}" ] || ( cd "$HERE/../../shell/mygoshell/native" && go test -c -o "$NATIVE" . )
( cd "$HERE/../../shell/mygoshell/native" && P2_MEASURE_DATA=$DATA P2_MEASURE_EXCHANGES=$N P2_MEASURE_OUT=$OUT/native.json \
    P2_GATE_PORT_FILE=$T/port P2_GATE_DONE_FILE=$T/done PIG_HOME=$(mktemp -d) "$NATIVE" -test.run TestMeasureLong -test.count=1 -test.timeout 55m >$OUT/native.log 2>&1 ) &
for _ in $(seq 1 36000); do [ -f "$T/port" ] && break; sleep 0.5; done
PORT=$(sed -n 1p "$T/port"); SID=$(sed -n 2p "$T/port")
node "$HERE/measure.mjs" "http://127.0.0.1:$PORT/s/$SID" "$N" "$OUT/web.json"
touch "$T/done"; wait
echo native:; cat "$OUT/native.json"; echo web:; cat "$OUT/web.json"
