# SPDX-License-Identifier: MIT
import http.client
import json
import os
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import proxy  # noqa: E402

SEEN = []


class Upstream(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_):
        return

    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        SEEN.append((self.path, self.headers.get("Authorization"), body, self.headers.get("Accept-Encoding")))
        sse = (b'data: {"choices":[{"delta":{"content":"hi"}}]}\n\n'
               b'data: {"choices":[],"usage":{"prompt_tokens":41,"completion_tokens":2},"timings":{"prompt_ms":12.5,"predicted_ms":3.0}}\n\ndata: [DONE]\n\n')
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(sse)))
        self.end_headers()
        self.wfile.write(sse)


class ProxyTest(unittest.TestCase):
    def setUp(self):
        SEEN.clear()
        self.up = ThreadingHTTPServer(("127.0.0.1", 0), Upstream)
        threading.Thread(target=self.up.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.up.server_address[1]}/v1"

    def tearDown(self):
        self.up.shutdown()
        self.up.server_close()

    def post(self, p, body, key="dummy"):
        conn = http.client.HTTPConnection("127.0.0.1", p.port, timeout=10)
        conn.request("POST", "/v1/chat/completions", json.dumps(body), {"Content-Type": "application/json", "Authorization": "Bearer " + key})
        response = conn.getresponse()
        try:
            return response.status, response.read()
        finally:
            conn.close()

    def test_forwards_unchanged_and_records_usage_sizes_and_times(self):
        body = {"messages": [{"role": "system", "content": "s" * 100}, {"role": "user", "content": "go"}], "tools": [{"type": "function", "function": {"name": "read"}}], "stream": True}
        with proxy.Proxy(self.url) as p:
            status, data = self.post(p, body)
            self.assertEqual(status, 200)
            self.assertIn(b"[DONE]", data)
        (record,) = p.recorder.snapshot()
        self.assertEqual(SEEN[0][0], "/v1/chat/completions")
        self.assertEqual(json.loads(SEEN[0][2]), body)  # the upstream got the request unchanged
        self.assertEqual((record["messages"], record["tools"], record["system_bytes"], record["stream"]), (2, 1, 100, True))
        self.assertEqual((record["prompt_tokens"], record["completion_tokens"], record["server_prompt_ms"]), (41, 2, 12.5))
        self.assertLessEqual(record["t_start"], record["t_first_byte"])
        self.assertLessEqual(record["t_first_byte"], record["t_end"])

    def test_records_never_hold_bodies_or_credentials(self):
        with proxy.Proxy(self.url) as p:
            self.post(p, {"messages": [{"role": "user", "content": "secret-prompt-text"}]}, key="sk-harness-key")
        dump = json.dumps(p.recorder.snapshot())
        for forbidden in ("secret-prompt-text", "sk-harness-key", "Authorization"):
            self.assertNotIn(forbidden, dump)

    def test_a_hosted_key_replaces_the_harness_key_upstream_only(self):
        with proxy.Proxy(self.url, api_key="hosted-key") as p:
            self.post(p, {"messages": []}, key="dummy")
        self.assertEqual(SEEN[0][1], "Bearer hosted-key")
        self.assertNotIn("hosted-key", json.dumps(p.recorder.snapshot()))

    def test_the_upstream_is_asked_for_an_uncompressed_reply(self):
        with proxy.Proxy(self.url) as p:
            conn = http.client.HTTPConnection("127.0.0.1", p.port, timeout=10)
            conn.request("POST", "/v1/chat/completions", "{}", {"Accept-Encoding": "gzip, br", "Content-Type": "application/json"})
            conn.getresponse().read()
            conn.close()
        self.assertEqual(SEEN[0][3], "identity")

    def test_usage_of_reads_the_responses_and_messages_wires(self):
        self.assertEqual(proxy.usage_of('data: {"type":"response.completed","response":{"usage":{"input_tokens":77,"output_tokens":5}}}')[0], {"input_tokens": 77, "output_tokens": 5})
        self.assertEqual(proxy.usage_of('data: {"type":"message_start","usage":{"input_tokens":9}}')[0], {"input_tokens": 9})

    def test_usage_of_merges_the_messages_stream_like_pi(self):
        # A real Messages stream: input_tokens only in message_start's message.usage, message_delta carries output_tokens (Pi anthropic-messages.ts:680, :827-840).
        stream = ('event: message_start\ndata: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":812,"output_tokens":1}}}\n\n'
                  'event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5,"input_tokens":null}}\n\n')
        self.assertEqual(proxy.usage_of(stream)[0], {"input_tokens": 812, "output_tokens": 5})

    def test_the_system_prompt_is_measured_on_every_wire(self):
        system = "You are a coding agent."
        chat = {"messages": [{"role": "system", "content": system}, {"role": "user", "content": "hi"}]}
        messages = {"system": [{"type": "text", "text": system}], "messages": [{"role": "user", "content": "hi"}]}
        codex = {"instructions": system, "input": [{"role": "user", "content": "hi"}]}
        self.assertEqual(proxy.summarize_request(json.dumps(chat))["system_bytes"], len(system))
        self.assertEqual(proxy.summarize_request(json.dumps(codex))["system_bytes"], len(system))
        self.assertEqual(proxy.summarize_request(json.dumps(messages))["system_bytes"], len(json.dumps(messages["system"])))

    def test_an_unreachable_upstream_is_recorded_as_an_error(self):
        with proxy.Proxy("http://127.0.0.1:9/v1") as p:
            status, _ = self.post(p, {"messages": []})
        self.assertEqual(status, 502)
        self.assertEqual(p.recorder.snapshot()[0]["error"], "ConnectionRefusedError")


if __name__ == "__main__":
    unittest.main()
