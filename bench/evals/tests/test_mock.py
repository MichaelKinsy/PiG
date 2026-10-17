# SPDX-License-Identifier: MIT
import http.client
import json
import sys
import time
import unittest

from pigbench import mock


def post(server, path, body):
    conn = http.client.HTTPConnection("127.0.0.1", server.port, timeout=10)
    conn.request("POST", path, json.dumps(body), {"Content-Type": "application/json"})
    return conn, conn.getresponse()


READ_TOOLS = {
    "/v1/chat/completions": {"tools": [{"type": "function", "function": {"name": "read", "parameters": {"properties": {"path": {}}}}}], "messages": [{"role": "user", "content": "hi"}]},
    "/v1/responses": {"tools": [{"type": "function", "name": "exec_command", "parameters": {"properties": {"cmd": {"type": "string"}}}}], "input": [{"role": "user", "content": "hi"}]},
    "/v1/messages": {"tools": [{"name": "Read", "input_schema": {"properties": {"file_path": {}}}}], "messages": [{"role": "user", "content": "hi"}]},
}


class MockTest(unittest.TestCase):
    def test_each_protocol_asks_for_a_tool_the_request_declares(self):
        for path, body in READ_TOOLS.items():
            with mock.MockServer(policy=mock.agentic_policy("/work", tool_turns=1)) as srv:
                conn, resp = post(srv, path, dict(body, stream=False))
                data = json.loads(resp.read())
                conn.close()
                text = json.dumps(data)
                self.assertIn("/work/README.md", text, path)
                self.assertEqual(srv.requests[0].protocol, mock.PROTOCOLS[path[3:]])

    def test_tool_round_counting_per_protocol(self):
        chat = mock.Request(0, "openai-completions", {"messages": [{"role": "user"}, {"role": "assistant"}, {"role": "tool"}, {"role": "assistant"}, {"role": "tool"}]}, 1, 0)
        self.assertEqual(chat.rounds(), 2)
        anth = mock.Request(0, "anthropic-messages", {"messages": [{"role": "user", "content": "q"}, {"role": "assistant", "content": []},
                                                                   {"role": "user", "content": [{"type": "tool_result", "tool_use_id": "x", "content": "y"}]}]}, 1, 0)
        self.assertEqual(anth.rounds(), 1)
        self.assertEqual(anth.roles, ["user", "assistant", "tool"])
        resp = mock.Request(0, "openai-responses", {"input": [{"role": "user"}, {"type": "function_call"}, {"type": "function_call_output"}]}, 1, 0)
        self.assertEqual(resp.rounds(), 1)

    def test_request_with_no_tools_gets_text(self):
        policy = mock.agentic_policy("/w", tool_turns=3)
        req = mock.Request(0, "openai-completions", {"messages": [{"role": "user", "content": "summarize"}]}, 1, 0)
        self.assertEqual(policy(req).tool_calls, [])
        self.assertIn("Summary", policy(req).text)

    def test_faults(self):
        body = dict(READ_TOOLS["/v1/chat/completions"], stream=True)
        with mock.MockServer(faults={0: mock.Fault(status=429, retry_after=0), 1: mock.Fault(drop_after=2), 2: mock.Fault(drop_before=True)}) as srv:
            conn, resp = post(srv, "/v1/chat/completions", body)
            self.assertEqual(resp.status, 429)
            self.assertEqual(resp.getheader("Retry-After"), "0")
            resp.read()
            conn.close()
            conn, resp = post(srv, "/v1/chat/completions", body)
            self.assertEqual(resp.status, 200)
            with self.assertRaises((http.client.IncompleteRead, ConnectionError, http.client.RemoteDisconnected)):
                resp.read()
            conn.close()
            conn, _ = http.client.HTTPConnection("127.0.0.1", srv.port, timeout=10), None
            conn.request("POST", "/v1/chat/completions", json.dumps(body))
            with self.assertRaises((ConnectionError, http.client.RemoteDisconnected)):
                conn.getresponse()
            self.assertEqual([r.fault.__class__.__name__ for r in srv.requests], ["Fault"] * 3)

    def reset_and_capture(self, send):
        """stderr of the mock while a client sends `send` on a fresh connection, then resets it; returns once the handler has finished."""
        import contextlib
        import io
        import socket
        import threading
        err = io.StringIO()
        with contextlib.redirect_stderr(err):
            with mock.MockServer() as srv:
                before = set(threading.enumerate())
                sock = socket.create_connection(("127.0.0.1", srv.port), timeout=10)
                send(sock)
                deadline = time.monotonic() + 10
                while not set(threading.enumerate()) - before and time.monotonic() < deadline:
                    time.sleep(0.01)  # the server thread accepts and starts the handler
                handlers = set(threading.enumerate()) - before
                self.assertTrue(handlers)
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, b"\x01\x00\x00\x00\x00\x00\x00\x00")
                sock.close()  # a reset
                for t in handlers:
                    t.join(10)
                self.assertFalse(any(t.is_alive() for t in handlers))
        return err.getvalue()

    def test_a_client_that_resets_its_idle_connection_is_not_a_mock_error(self):
        # Every harness that exits with an idle keep-alive connection did this on Windows: one traceback per run, 902 in one overhead run.
        body = json.dumps(READ_TOOLS["/v1/chat/completions"]).encode()

        def one_request(sock):
            sock.sendall(b"POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nContent-Length: %d\r\n\r\n" % len(body) + body)
            reply = b""
            while b"\r\n\r\n" not in reply or not reply.endswith(b"}"):
                chunk = sock.recv(65536)
                self.assertTrue(chunk, f"the mock closed the connection after {reply!r}")
                reply += chunk  # once the reply is complete the server waits for the next request on this connection

        self.assertNotIn("Traceback", self.reset_and_capture(one_request))

    def test_a_reset_in_the_middle_of_a_request_is_still_reported(self):
        def half_a_body(sock):
            sock.sendall(b"POST /v1/chat/completions HTTP/1.1\r\nHost: x\r\nContent-Length: 1000\r\n\r\n{")
            time.sleep(0.2)  # the handler is reading the body

        self.assertIn("ConnectionResetError", self.reset_and_capture(half_a_body))

    @unittest.skipUnless(sys.platform == "win32", "a Windows reset discards unread bytes; elsewhere the drop is a reset")
    def test_a_mid_stream_drop_delivers_the_events_sent_before_it(self):
        body = dict(READ_TOOLS["/v1/chat/completions"], stream=True)
        for _ in range(20):  # a reset raced the client's first read in about one run in three
            with mock.MockServer(faults={0: mock.Fault(drop_after=2)}) as srv:
                conn, resp = post(srv, "/v1/chat/completions", body)
                self.assertEqual(resp.status, 200)
                time.sleep(0.05)  # the drop has happened by now
                with self.assertRaises(http.client.IncompleteRead) as caught:
                    resp.read()
                conn.close()
            self.assertEqual(caught.exception.partial.count(b"data: "), 2)

    def test_stream_events_carry_usage_and_finish_reason(self):
        with mock.MockServer(policy=mock.scripted_policy([mock.Step(text="a b c")])) as srv:
            conn, resp = post(srv, "/v1/chat/completions", dict(READ_TOOLS["/v1/chat/completions"], stream=True))
            raw = resp.read().decode()
            conn.close()
        events = [json.loads(line[6:]) for line in raw.splitlines() if line.startswith("data: ") and line != "data: [DONE]"]
        self.assertEqual("".join(e["choices"][0]["delta"].get("content", "") for e in events if e["choices"]), "a b c")
        self.assertEqual(events[-2]["choices"][0]["finish_reason"], "stop")
        self.assertIn("prompt_tokens", events[-1]["usage"])



class KeepAliveLatencyTest(unittest.TestCase):
    """A harness that reuses its connection (PiG's shared transport, Pi's undici dispatcher) must not wait on the mock.

    The mock writes a streamed response in several small sends. With Nagle's algorithm on, every send after the first
    waits for the client's ACK, and a client past the start of a connection delays its ACK by up to 40 ms on Linux, so
    each turn on a reused connection gained about 40 ms of idle that no harness causes.
    """

    def test_the_mock_disables_nagle_on_every_connection(self):
        with mock.MockServer(policy=mock.agentic_policy("/work", tool_turns=1)) as srv:
            conn, resp = post(srv, "/v1/chat/completions", dict(READ_TOOLS["/v1/chat/completions"], stream=False))
            resp.read()
            self.assertTrue(srv.httpd.RequestHandlerClass.disable_nagle_algorithm)
            conn.close()

    def test_streamed_turns_on_one_connection_do_not_wait_for_a_delayed_ack(self):
        import time
        with mock.MockServer(policy=mock.agentic_policy("/work", tool_turns=8)) as srv:
            conn = http.client.HTTPConnection("127.0.0.1", srv.port, timeout=10)
            body = dict(READ_TOOLS["/v1/chat/completions"], stream=True)
            waits = []
            for _ in range(8):
                start = time.monotonic()
                conn.request("POST", "/v1/chat/completions", json.dumps(body), {"Content-Type": "application/json"})
                conn.getresponse().read()
                waits.append(time.monotonic() - start)
                body["messages"] = body["messages"] + [{"role": "assistant"}, {"role": "tool"}]
            conn.close()
            waits.sort()
            # A delayed ACK costs 40 ms per turn; an idle loopback turn costs well under 1 ms, so 20 ms separates them on any host.
            self.assertLess(waits[len(waits) // 2], 0.020, waits)


if __name__ == "__main__":
    unittest.main()
