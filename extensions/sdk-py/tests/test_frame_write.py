"""A frame the Python SDK sends is one socket write: its 4-byte length and its JSON together."""

import json
import struct
import unittest

import pig_sdk


class _RecordingSocket:
    def __init__(self):
        self.writes = []

    def sendall(self, data):
        self.writes.append(bytes(data))


class FrameWriteTests(unittest.TestCase):
    def test_a_frame_is_one_socket_write(self):
        ext = pig_sdk.Extension("frame-write")
        sock = _RecordingSocket()
        ext._sock = sock
        ext._send_locked({"type": "pong", "pong": {"nonce": "n1"}})
        self.assertEqual(len(sock.writes), 1, "the header and the body were written apart")
        frame = sock.writes[0]
        (length,) = struct.unpack(">I", frame[:4])
        self.assertEqual(length, len(frame) - 4)
        self.assertEqual(json.loads(frame[4:]), {"type": "pong", "pong": {"nonce": "n1"}})

    def test_an_oversized_frame_is_refused_before_any_write(self):
        ext = pig_sdk.Extension("frame-write")
        sock = _RecordingSocket()
        ext._sock = sock
        with self.assertRaises(RuntimeError):
            ext._send_locked({"type": "pong", "pong": {"nonce": "x" * (pig_sdk.MAX_FRAME_SIZE + 1)}})
        self.assertEqual(sock.writes, [])


if __name__ == "__main__":
    unittest.main()
