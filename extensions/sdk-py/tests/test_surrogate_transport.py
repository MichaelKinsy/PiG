import json
import socket
import struct

import pig_sdk
from test_request_lifetime import connected_extension


def test_wire_preserves_pi_utf16_units_and_literal_escapes():
    # Pi stdin-buffer.ts:251 exposes lone halves; JSON.stringify preserves them.
    left, right = socket.socketpair()
    ext = pig_sdk.Extension("surrogate-wire")
    ext._sock = left
    try:
        for text in ("", "A", "\ud83d", "\ude00", "😀", "A\ud83dZ\ude00", r"\ud83d"):
            frame = {"text": text, "lines": [text], "nested": {text: text}}
            ext._send(frame)
            size = struct.unpack(">I", right.recv(4))[0]
            raw = bytearray()
            while len(raw) < size:
                raw.extend(right.recv(size - len(raw)))
            assert json.loads(raw) == frame
            raw.decode("utf-8", errors="strict")
            right.sendall(struct.pack(">I", len(raw)) + raw)
            assert ext._read() == frame
    finally:
        left.close()
        right.close()


def test_terminal_dispatch_round_trips_lone_units():
    ext = pig_sdk.Extension("surrogate-dispatch")
    # The connection helper's no-UI ready state is irrelevant to the serializer;
    # use the same subscription primitive the public UI binding calls.
    ext.command("subscribe", "Subscribe", lambda ctx, _: ext._add_terminal_input_handler(
        lambda data: pig_sdk.TerminalInputResult(data="seen:" + data)
    ))
    with connected_extension(ext) as (send, receive):
        send({"type": "request", "id": "subscribe", "request": {"method": "command", "tool": "subscribe"}})
        call = receive("call")
        assert call["call"]["method"] == "ui.onTerminalInput"
        send({"type": "call_result", "id": call["id"], "call_result": {}})
        receive("response")
        for text in ("\ud83d", "\ude00", "😀", r"\ud83d"):
            send({"type": "request", "id": "input", "request": {"method": "terminal_input", "args": {"data": text, "editorText": "A\ud83d"}}})
            result = receive("response")["response"]["result"]
            assert result == {"consume": False, "data": "seen:" + text}
