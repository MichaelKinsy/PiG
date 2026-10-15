# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
"""Tests for fake_server.py: the fake model's port is bound before the server process exists and stays its own.

  python3 -m unittest discover -s bench/compare/tests -p 'test_fake_server.py'
"""
import concurrent.futures
import errno
import json
import os
import socket
import sys
import tempfile
import unittest
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

import fake_server  # noqa: E402

MOCK = HERE / "fake_model_ctx.py"


def request(port, path="/v1/models", body=None):
    req = urllib.request.Request(f"http://127.0.0.1:{port}{path}", data=body, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=30) as resp:
        return resp.read()


def listening_inode(port):
    """The socket inode listening on 127.0.0.1:PORT, from /proc/net/tcp (state 0A = LISTEN)."""
    want = f"0100007F:{port:04X}"
    for line in Path("/proc/net/tcp").read_text().splitlines()[1:]:
        fields = line.split()
        if fields[1] == want and fields[3] == "0A":
            return fields[9]
    return None


def socket_inodes(pid):
    out = set()
    for fd in os.listdir(f"/proc/{pid}/fd"):
        try:
            target = os.readlink(f"/proc/{pid}/fd/{fd}")
        except OSError:
            continue
        if target.startswith("socket:["):
            out.add(target[len("socket:["):-1])
    return out


class FakeServerTest(unittest.TestCase):
    def test_the_port_cannot_be_taken_once_chosen(self):
        # The old free_port() released the port it chose, so any process could bind it before the server did.
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            released = probe.getsockname()[1]
        with socket.socket() as other:
            other.bind(("127.0.0.1", released))
        sock = fake_server.listener()
        try:
            with socket.socket() as other, self.assertRaises(OSError) as caught:
                other.bind(("127.0.0.1", sock.getsockname()[1]))
            self.assertEqual(caught.exception.errno, errno.EADDRINUSE)
        finally:
            sock.close()

    @unittest.skipUnless(os.path.exists("/proc/net/tcp"), "needs Linux /proc")
    def test_the_server_process_owns_the_listener_it_was_handed(self):
        proc, port = fake_server.start(MOCK)
        try:
            self.assertEqual(json.loads(request(port))["data"][0]["id"], "fake-model")
            inode = listening_inode(port)
            self.assertIsNotNone(inode)
            self.assertIn(inode, socket_inodes(proc.pid))
            self.assertNotIn(inode, socket_inodes(os.getpid()))
        finally:
            proc.terminate()
            proc.wait()

    def test_parallel_samples_each_reach_their_own_server(self):
        body = json.dumps({"model": "fake-model", "stream": True, "messages": [{"role": "user", "content": "hi"}]}).encode()

        def sample(index):
            with tempfile.TemporaryDirectory() as d:
                log = Path(d) / "requests.jsonl"
                with fake_server.serve(MOCK, "--log", log) as port:
                    request(port, "/v1/chat/completions", body)
                return port, len(log.read_text().splitlines())

        with concurrent.futures.ThreadPoolExecutor(32) as pool:
            results = list(pool.map(sample, range(64)))
        self.assertEqual([n for _, n in results], [1] * 64, "a sample's request reached another sample's server")

    def test_a_server_that_exits_is_reported(self):
        with tempfile.TemporaryDirectory() as d:
            script = Path(d) / "exits.py"
            script.write_text("raise SystemExit(3)\n")
            with self.assertRaisesRegex(RuntimeError, "did not start"), fake_server.serve(script):
                pass


if __name__ == "__main__":
    unittest.main()
