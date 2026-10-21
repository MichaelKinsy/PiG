# SPDX-License-Identifier: MIT
import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import vtscreen  # noqa: E402
import ptyprobe  # noqa: E402


class QueryClassification(unittest.TestCase):
    def queries(self, data):
        return [raw for _, raw in vtscreen.Screen(10, 40).feed(data)]

    def test_the_queries_both_programs_send(self):
        sent = [b"\x1b[c", b"\x1b[>0q", b"\x1b[?u", b"\x1b[?2026$p", b"\x1b[?2;1;0S", b"\x1b_Gi=1,a=q\x1b\\",
                b"\x1b]10;?\x07", b"\x1b]11;?\x07", b"\x1b]4;3;?\x07", b"\x1b]7501;?\x1b\\"]
        self.assertEqual(self.queries(b"".join(sent)), sent)

    def test_mode_sets_and_text_are_not_queries(self):
        data = b"\x1b[?1049h\x1b[m\x1b[H\x1b[J\x1b[?25l\x1b[?2004h\x1b[>4;2m\x1b[?1002;1003;1004;1006h\x1b[38;2;0;1;2mhi\x1b[0m"
        screen = vtscreen.Screen(10, 40)
        self.assertEqual(screen.feed(data), [])
        self.assertEqual(screen.glyphs(), 2)

    def test_query_split_across_chunks(self):
        screen = vtscreen.Screen(10, 40)
        self.assertEqual(screen.feed(b"ab\x1b["), [])
        self.assertEqual([raw for _, raw in screen.feed(b"c")], [b"\x1b[c"])


class Grid(unittest.TestCase):
    def test_cursor_erase_and_scroll(self):
        screen = vtscreen.Screen(3, 5)
        screen.feed(b"\x1b[2;3Hxy\x1b[2;1H\x1b[2Kz\x1b[1;1Hab\r\ncd\r\nef\r\ngh")
        self.assertEqual(screen.text().split("\n"), ["cd", "ef", "gh"])
        screen.feed(b"\x1b[2J")
        self.assertEqual(screen.glyphs(), 0)

    def test_wide_and_utf8_split(self):
        screen = vtscreen.Screen(2, 6)
        data = "\u2584a\u754c".encode()
        screen.feed(data[:2])
        screen.feed(data[2:])
        self.assertEqual(screen.text().split("\n")[0], "\u2584a\u754c")

    def test_synchronized_output_counts_frames(self):
        screen = vtscreen.Screen(2, 6)
        screen.feed(b"\x1b[?2026hx\x1b[?2026l\x1b[?2026hy\x1b[?2026l")
        self.assertEqual(screen.esu_count, 2)


class Answers(unittest.TestCase):
    def test_replies_in_stream_order_and_split_queries(self):
        class Fd:
            written = b""

        written = []
        real = os.write
        os.write = lambda fd, data: written.append(data)
        try:
            tail = ptyprobe.answer(1, b"\x1b]10;?\x07\x1b]11;?\x07\x1b[", b"")
            self.assertEqual(written, [ptyprobe.OSC11_REPLY])
            ptyprobe.answer(1, b"c", tail)
            self.assertEqual(written[1], ptyprobe.DA1_REPLY)
        finally:
            os.write = real


class Stats(unittest.TestCase):
    def test_stats(self):
        st = ptyprobe.stats([5, 1, 3, None, 2, 4])
        self.assertEqual((st["n"], st["median"], st["min"], st["max"], st["p90"]), (5, 3, 1, 5, 5))


if __name__ == "__main__":
    unittest.main()
