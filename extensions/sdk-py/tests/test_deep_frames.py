"""Arguments nested thousands of levels deep (the host sends up to 10000) are read and written on every interpreter the SDK supports (3.11's JSON reader takes about 990, 3.12's 9997), and a frame nested past what the host can send is answered with Pi's RangeError, not the end of the connection."""

import contextlib
import io
import json
import socket
import struct
import tempfile
import threading
import unittest

import pig_sdk
from pig_sdk import TOO_DEEP_MESSAGE, _frame_head, _js_ordered, _loads_deep, _too_deep_envelope
from pig_sdk.serialize_conversation import _json_stringify


def _deep(depth: int):
    value = 1
    for _ in range(depth):
        value = [value]
    return value


def _nested_text(depth: int) -> str:
    return "[" * depth + "1" + "]" * depth


# The deepest argument the host sends: its marshal refuses a value nested past 10000 levels, and the envelope around the arguments takes a few.
HOST_DEPTH = 9990


def _frame(depth: int, envelope: str) -> bytes:
    body = envelope.replace("DEEP", _nested_text(depth)).encode()
    return struct.pack(">I", len(body)) + body


def _frame_with_args(depth: int, kind: str, request: str) -> bytes:
    return _frame(depth, '{"type":"%s","id":"c-1","%s":%s}' % (kind, kind, request))


class _Host:
    """The host's end of a connection to an extension that runs on a thread: raw frames in, decoded frames out."""

    def __init__(self, ext: pig_sdk.Extension):
        self._dir = tempfile.TemporaryDirectory()
        listener = socket.socket(socket.AF_UNIX)
        listener.bind(self._dir.name + "/sdk.sock")
        listener.listen(1)
        self.ext = ext
        self.errors = io.StringIO()
        self._runtime = threading.Thread(target=ext.run_with_socket, args=(self._dir.name + "/sdk.sock",), daemon=True)
        self._runtime.start()
        self.sock, _ = listener.accept()
        listener.close()
        self.sock.settimeout(20)

    def send(self, frame: bytes) -> None:
        self.sock.sendall(frame)

    def receive(self, kind: str) -> dict:
        while True:
            size = struct.unpack(">I", self._exact(4))[0]
            frame = _loads_deep(self._exact(size))
            if frame["type"] == kind:
                return frame

    def _exact(self, size: int) -> bytes:
        chunks = bytearray()
        while len(chunks) < size:
            chunk = self.sock.recv(size - len(chunks))
            assert chunk, "SDK disconnected"
            chunks.extend(chunk)
        return bytes(chunks)

    def close(self) -> None:
        self.sock.close()
        self._runtime.join(10)
        self._dir.cleanup()
        assert not self._runtime.is_alive()


def _depth_of(value) -> int:
    depth = 0
    while isinstance(value, (list, dict)):
        value = value[0] if isinstance(value, list) else [member for member in value.values() if isinstance(member, (list, dict))][0]
        depth += 1
    return depth


class DeepFrameTests(unittest.TestCase):
    def test_a_value_nested_past_the_interpreters_limit_is_ordered_and_stringified(self):
        for depth in (200, 1000, 4000, 9000, HOST_DEPTH):
            value = {"7": "s", "zeta": 1, "meta": {"d": _deep(depth)}}
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

    def test_a_frame_the_host_can_send_is_read_whole(self):
        near, far = socket.socketpair()
        self.addCleanup(near.close)
        self.addCleanup(far.close)
        ext = pig_sdk.Extension("deep-read")
        ext._sock = near
        for depth in (500, 1500, 9000, HOST_DEPTH):
            frame = _frame(depth, '{"type":"request","id":"c-3","request":{"method":"tool_call","args":{"7":"s","d":DEEP}}}')
            writer = threading.Thread(target=far.sendall, args=(frame,))
            writer.start()
            try:
                env = ext._read()
            finally:
                writer.join()
            self.assertEqual((env["type"], env["id"]), ("request", "c-3"))
            args = env["request"]["args"]
            self.assertEqual(list(args), ["7", "d"])
            self.assertEqual(_depth_of(args["d"]), depth)

    def test_the_deep_reader_is_json_loads(self):
        for text in ('{"a":[1,2.5,-0,true,false,null,"x\\u00e9\\ud800"],"b":{},"c":[],"d":{"e":[{}]}} ', "[[],[[]]]", '"s"', "12", "NaN", '{"a":1,"a":2}', '[\n1 ,\t2\r]'):
            self.assertEqual(repr(_loads_deep(text.encode())), repr(json.loads(text)), text)
        for text in ("", "[", "[1,]", "[1 2]", '{"a"}', '{"a":1,}', "{1:2}", "[1]x", '{"a":}', "]", "tru"):
            with self.assertRaises(ValueError, msg=text):
                _loads_deep(text.encode())

    def test_a_frame_past_what_the_host_can_send_is_refused_not_read(self):
        with self.assertRaises(RecursionError):
            _loads_deep(_nested_text(pig_sdk.MAX_FRAME_DEPTH + 5).encode())
        self.assertEqual(_depth_of(_loads_deep(_nested_text(pig_sdk.MAX_FRAME_DEPTH).encode())), pig_sdk.MAX_FRAME_DEPTH)

    def test_deep_values_are_written_and_checked(self):
        value = {"a": [_deep(HOST_DEPTH), {"b": float("nan"), "7": None}], 3: True}
        text = pig_sdk._dumps_deep(value)
        self.assertEqual(text, '{"a":[' + _nested_text(HOST_DEPTH) + ',{"b":null,"7":null}],"3":true}')
        self.assertEqual(pig_sdk._json_text(value), text)
        pig_sdk._ensure_jsonable(value, "value")
        for sample in ({"k": [1, "é\n", 2.5, {"x": None}], "n": {}, "e": [], True: False}, [1, [2, [3]]], "s", 3):
            self.assertEqual(pig_sdk._dumps_deep(sample), json.dumps(sample, separators=(",", ":")))
        with self.assertRaises(TypeError):
            pig_sdk._dumps_deep([_deep(30), object()])
        with self.assertRaises(RecursionError):
            pig_sdk._dumps_deep(_deep(pig_sdk.MAX_FRAME_DEPTH + 5))
        with self.assertRaises(TypeError):
            pig_sdk._ensure_jsonable([_deep(HOST_DEPTH), object()], "value")
        self.assertTrue(pig_sdk._holds_bigint({"a": [_deep(HOST_DEPTH), pig_sdk.BigInt(1)]}))
        self.assertFalse(pig_sdk._holds_bigint(_deep(HOST_DEPTH)))
        circular: list = []
        circular.append(circular)
        with self.assertRaises(RecursionError):
            pig_sdk._dumps_deep(circular)

    def test_a_deep_event_payload_is_copied_in_order_and_apart(self):
        original = {"b": [_deep(HOST_DEPTH), "x"], "a": {"k": 1}}
        copy = pig_sdk._clone_stream_value(original)
        self.assertEqual(list(copy), ["b", "a"])
        self.assertEqual(_depth_of(copy["b"][0]), HOST_DEPTH)
        self.assertIsNot(copy["b"], original["b"])
        self.assertIsNot(copy["b"][0], original["b"][0])
        self.assertEqual((copy["b"][1], copy["a"]), ("x", {"k": 1}))

    def test_a_deep_tool_call_reaches_the_tool_and_its_reply_goes_back(self):
        seen = []
        ext = pig_sdk.Extension("deep-tool")

        def tool(ctx, args):
            seen.append(_depth_of(args["d"]))
            return {"echo": args}

        ext.tool("deep", "Deep", {"type": "object"}, tool)
        host = _Host(ext)
        try:
            host.receive("register")
            host.send(_frame(0, '{"type":"ready","ready":{}}'))
            host.send(_frame(HOST_DEPTH - 3, '{"type":"request","id":"c-1","request":{"method":"tool_call","tool":"deep","tool_call_id":"t1","args":{"7":1,"d":DEEP}}}'))
            reply = host.receive("response")["response"]
            self.assertIsNone(reply["error"], reply["error"])
            self.assertEqual(seen, [HOST_DEPTH - 3])
            self.assertEqual(_depth_of(reply["result"]), HOST_DEPTH - 3 + 2)
            host.send(_frame(0, '{"type":"ping","ping":{"nonce":"n"}}'))
            self.assertEqual(host.receive("pong")["pong"], {"nonce": "n"})
        finally:
            host.close()

    def test_a_deep_tool_call_event_input_and_a_deep_model_stream_notify(self):
        inputs = []
        ext = pig_sdk.Extension("deep-event")

        def on_tool_call(ctx, event):
            inputs.append(_depth_of(event["input"]))
            event["input"]["edited"] = True

        ext.on_event("tool_call", on_tool_call)
        host = _Host(ext)
        try:
            host.receive("register")
            host.send(_frame(0, '{"type":"ready","ready":{}}'))
            host.send(_frame(HOST_DEPTH - 4, '{"type":"request","id":"e-1","request":{"method":"event","event":"tool_call","handler_id":1,"args":{"type":"tool_call","toolName":"deep","toolCallId":"t1","input":{"d":DEEP}}}}'))
            reply = host.receive("response")["response"]
            self.assertIsNone(reply["error"], reply["error"])
            self.assertEqual(inputs, [HOST_DEPTH - 4 + 1])
            self.assertEqual(reply["result"]["_pigToolCallInput"]["edited"], True)
            host.send(_frame(HOST_DEPTH - 4, '{"type":"notify","notify":{"method":"model_stream_event","args":{"d":DEEP}}}'))
            host.send(_frame(0, '{"type":"ping","ping":{"nonce":"m"}}'))
            self.assertEqual(host.receive("pong")["pong"], {"nonce": "m"})
        finally:
            host.close()

    def test_a_notify_past_the_reader_is_dropped_with_one_line_on_stderr(self):
        near, far = socket.socketpair()
        self.addCleanup(near.close)
        self.addCleanup(far.close)
        ext = pig_sdk.Extension("deep-notify")
        ext._sock = near
        captured = io.StringIO()
        frame = _frame(pig_sdk.MAX_FRAME_DEPTH + 50, '{"type":"notify","notify":{"method":"model_stream_event","args":DEEP}}')
        writer = threading.Thread(target=far.sendall, args=(frame + _frame(0, '{"type":"ping","ping":{"nonce":"n"}}'),))
        writer.start()
        try:
            with contextlib.redirect_stderr(captured):
                first = ext._read()
                second = ext._read()
        finally:
            writer.join()
        self.assertEqual(first, {"type": "refused"})
        self.assertEqual(second, {"type": "ping", "ping": {"nonce": "n"}})
        self.assertEqual(captured.getvalue(), "pig_sdk: dropped a notify frame nested deeper than the reader takes\n")

    def test_the_serve_loop_goes_on_after_a_notify_and_answers_a_request_past_the_reader(self):
        ext = pig_sdk.Extension("deep-serve")
        host = _Host(ext)
        captured = io.StringIO()
        try:
            host.receive("register")
            host.send(_frame(0, '{"type":"ready","ready":{}}'))
            with contextlib.redirect_stderr(captured):
                host.send(_frame(pig_sdk.MAX_FRAME_DEPTH + 50, '{"type":"notify","notify":{"method":"model_stream_event","args":DEEP}}'))
                host.send(_frame(pig_sdk.MAX_FRAME_DEPTH + 50, '{"type":"request","id":"r-1","request":{"method":"tool_call","tool":"x","args":DEEP}}'))
                response = host.receive("response")
                host.send(_frame(0, '{"type":"ping","ping":{"nonce":"n"}}'))
                self.assertEqual(host.receive("pong")["pong"], {"nonce": "n"})
            self.assertEqual((response["id"], response["response"]["error"]), ("r-1", {"message": TOO_DEEP_MESSAGE}))
            self.assertEqual(captured.getvalue(), "pig_sdk: dropped a notify frame nested deeper than the reader takes\n")
        finally:
            host.close()

    def test_a_request_past_the_reader_while_the_extension_loads_is_answered_and_the_factory_goes_on(self):
        near, far = socket.socketpair()
        self.addCleanup(near.close)
        self.addCleanup(far.close)
        ext = pig_sdk.Extension("deep-load")
        ext._sock = near
        deep = pig_sdk.MAX_FRAME_DEPTH + 50
        writer = threading.Thread(
            target=far.sendall,
            args=(
                _frame_with_args(deep, "request", '{"method":"tool_call","args":DEEP}')
                + _frame(deep, '{"type":"notify","notify":{"args":DEEP}}')
                + _frame(0, '{"type":"ready","ready":{}}'),
            ),
        )
        writer.start()
        captured = io.StringIO()
        try:
            with contextlib.redirect_stderr(captured):
                ready = ext._read_during_load()
        finally:
            writer.join()
        self.assertEqual(ready["type"], "ready")
        answer = {}
        while answer.get("type") != "response":
            answer = _loads_deep(far.recv(struct.unpack(">I", far.recv(4))[0]))
        self.assertEqual((answer["type"], answer["id"], answer["response"]["error"]), ("response", "c-1", {"message": TOO_DEEP_MESSAGE}))
        self.assertEqual(captured.getvalue(), "pig_sdk: dropped a notify frame nested deeper than the reader takes\n")


if __name__ == "__main__":
    unittest.main()
