"""Arguments nested thousands of levels deep are read and written, and a frame nested past the interpreter's JSON reader is answered with Pi's RangeError, not the end of the connection."""

import json
import socket
import struct
import threading
import unittest

import pig_sdk
from pig_sdk import TOO_DEEP_MESSAGE, _frame_head, _js_ordered, _too_deep_envelope
from pig_sdk.serialize_conversation import _json_stringify


def _deep(depth: int):
    value = 1
    for _ in range(depth):
        value = [value]
    return value


def _nested_text(depth: int) -> str:
    return "[" * depth + "1" + "]" * depth


class DeepFrameTests(unittest.TestCase):
    def test_a_value_nested_past_the_interpreters_limit_is_ordered_and_stringified(self):
        for depth in (200, 1000, 4000, 9000):
            value = {"7": "s", "zeta": 1, "meta": {"d": json.loads(_nested_text(depth))}}
            ordered = _js_ordered(value)
            self.assertEqual(list(ordered), ["7", "zeta", "meta"])
            self.assertEqual(_json_stringify(ordered), '{"7":"s","zeta":1,"meta":{"d":' + _nested_text(depth) + "}}")

    def test_ordering_and_stringifying_are_those_of_javascript(self):
        value = {"b": [1, {"y": None, "10": True, "2": "q\"", "z": 1.5}], "a": "x"}
        self.assertEqual(_json_stringify(value), '{"b":[1,{"2":"q\\"","10":true,"y":null,"z":1.5}],"a":"x"}')
        self.assertEqual(list(_js_ordered(value["b"][1])), ["2", "10", "y", "z"])

    def test_the_head_of_a_frame_is_read_without_decoding_what_nests(self):
        body = ('{"type":"request","id":"c-7","request":{"method":"tool_call","args":' + _nested_text(30000) + ',"s":"]}"}}').encode()
        self.assertEqual(_frame_head(body), ("request", "c-7"))
        self.assertEqual(_frame_head(b'{"id":"a\\"b","pad":[{"type":"x"}],"type":"call_result"}'), ("call_result", 'a"b'))
        self.assertEqual(_frame_head(b"[1]"), (None, None))

    def test_a_frame_the_reader_cannot_nest_is_a_refused_request_and_the_connection_goes_on(self):
        near, far = socket.socketpair()
        self.addCleanup(near.close)
        self.addCleanup(far.close)
        ext = pig_sdk.Extension("deep-frame")
        ext._sock = near
        for body, want in (
            (('{"type":"request","id":"c-1","request":{"method":"tool_call","args":' + _nested_text(40000) + "}}").encode(), {"type": "request", "id": "c-1", "refused": TOO_DEEP_MESSAGE}),
            (('{"type":"call_result","id":"h-2","call_result":{"result":' + _nested_text(40000) + "}}").encode(), {"type": "call_result", "id": "h-2", "call_result": {"error": {"message": TOO_DEEP_MESSAGE}}}),
            (('{"type":"notify","notify":{"args":' + _nested_text(40000) + "}}").encode(), {"type": "refused"}),
            (b'{"type":"ping","ping":{"nonce":"n"}}', {"type": "ping", "ping": {"nonce": "n"}}),
        ):
            writer = threading.Thread(target=far.sendall, args=(struct.pack(">I", len(body)) + body,))
            writer.start()
            try:
                self.assertEqual(ext._read(), want)
            finally:
                writer.join()
        self.assertEqual(_too_deep_envelope(b"{}"), {"type": "refused"})


if __name__ == "__main__":
    unittest.main()
