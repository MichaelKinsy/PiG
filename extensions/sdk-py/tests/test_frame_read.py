"""A frame the Python SDK reads arrives whole however the socket splits it, and a closed socket is reported where it closed."""

import socket
import struct
import threading
import unittest

import pig_sdk


def _frame(body: bytes) -> bytes:
    return struct.pack(">I", len(body)) + body


class FrameReadTests(unittest.TestCase):
    def setUp(self):
        self.near, self.far = socket.socketpair()
        self.addCleanup(self.near.close)
        self.addCleanup(self.far.close)
        self.ext = pig_sdk.Extension("frame-read")
        self.ext._sock = self.near

    def test_a_frame_split_across_many_writes_arrives_whole(self):
        # Multi-byte text proves the body is decoded as one UTF-8 string, not per chunk.
        body = '{"type":"ping","text":"日本語😀"}'.encode()
        frame = _frame(body)

        def writer():
            for i in range(0, len(frame), 3):
                self.far.sendall(frame[i : i + 3])

        thread = threading.Thread(target=writer)
        thread.start()
        try:
            self.assertEqual(self.ext._read(), {"type": "ping", "text": "日本語😀"})
        finally:
            thread.join()

    def test_a_large_frame_arrives_whole(self):
        text = "x" * (3 << 20)
        body = ('{"text":"%s"}' % text).encode()
        thread = threading.Thread(target=lambda: self.far.sendall(_frame(body)))
        thread.start()
        try:
            self.assertEqual(self.ext._read(), {"text": text})
        finally:
            thread.join()

    def test_two_frames_in_one_segment_are_read_one_at_a_time(self):
        self.far.sendall(_frame(b'{"n":1}') + _frame(b'{"n":2}'))
        self.assertEqual([self.ext._read(), self.ext._read()], [{"n": 1}, {"n": 2}])

    def test_a_close_before_the_header_is_end_of_file(self):
        self.far.close()
        with self.assertRaises(EOFError):
            self.ext._read()

    def test_a_close_inside_a_frame_is_end_of_file_not_a_short_frame(self):
        self.far.sendall(struct.pack(">I", 100) + b'{"a":')
        self.far.close()
        with self.assertRaises(EOFError):
            self.ext._read()

    def test_a_close_inside_the_header_is_end_of_file(self):
        self.far.sendall(b"\x00\x00")
        self.far.close()
        with self.assertRaises(EOFError):
            self.ext._read()

    def test_a_header_above_the_frame_limit_is_refused(self):
        # The peer closes after the header, so a reader that skipped the limit fails with EOFError instead of waiting for 128 MiB that never come.
        self.far.sendall(struct.pack(">I", pig_sdk.MAX_FRAME_SIZE + 1))
        self.far.close()
        with self.assertRaises(RuntimeError):
            self.ext._read()


if __name__ == "__main__":
    unittest.main()
