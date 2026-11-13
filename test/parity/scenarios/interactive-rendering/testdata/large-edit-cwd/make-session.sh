#!/bin/sh
# Writes into ./sessions a session whose edit result holds a diff of 190 000 short added lines: 1.5 times the row count at which
# Pi before 5b5be2c316 failed with "RangeError: Maximum call stack size exceeded" when it drew the result (about 2 MB).
# The session is generated in the run's own cwd copy, so it is not in git.
set -eu
mkdir -p sessions
out=sessions/2025-01-01T00-00-00-000Z_parity-large-edit.jsonl
usage='"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}'
{
  printf '%s\n' '{"type":"session","version":3,"id":"parity-large-edit","timestamp":"2025-01-01T00:00:00.000Z","cwd":"."}'
  printf '%s\n' '{"type":"message","id":"c0000001","parentId":null,"timestamp":"2025-01-01T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"LARGE-EDIT-START"}]}}'
  printf '%s\n' '{"type":"message","id":"c0000002","parentId":"c0000001","timestamp":"2025-01-01T00:00:02.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"edit-call","name":"edit","arguments":{"path":"export.html","edits":[{"oldText":"OLD","newText":"NEW"}]}}],"api":"faux","provider":"test-faux","model":"faux-1",'"$usage"',"stopReason":"toolUse","timestamp":1735689602000}}'
  printf '%s' '{"type":"message","id":"c0000003","parentId":"c0000002","timestamp":"2025-01-01T00:00:03.000Z","message":{"role":"toolResult","toolCallId":"edit-call","toolName":"edit","content":[{"type":"text","text":"Successfully replaced 1 block(s) in export.html."}],"details":{"diff":"'
  awk 'BEGIN { for (i = 1; i < 190000; i++) printf "+%d a\\n", i }'
  printf '%s\n' '+190000 LARGE-EDIT-LAST","firstChangedLine":1},"isError":false,"timestamp":1735689603000}}'
  printf '%s\n' '{"type":"message","id":"c0000004","parentId":"c0000003","timestamp":"2025-01-01T00:00:04.000Z","message":{"role":"assistant","content":[{"type":"text","text":"LARGE-EDIT-END"}],"api":"faux","provider":"test-faux","model":"faux-1",'"$usage"',"stopReason":"stop","timestamp":1735689604000}}'
} > "$out"
echo "LARGE-EDIT-SESSION-$(printf WRITTEN)"
