"""Truncation helpers against values from Pi's core/tools/truncate.ts (v1.1.0) run in Node.

TestConformance_TruncationHelpersAgreeAcrossSDKs covers the shared cases. These cover inputs the Go-driven conformance wire cannot carry: lone
surrogates, which the Python wire preserves like Pi does, and negative limits, which the Rust SDK cannot represent.
"""

import pig_sdk


def test_tail_partial_line_replaces_a_lone_surrogate_like_buffer_from():
    # Pi: truncateTail("ab\ud800cd", {maxBytes: 6}) keeps "b\ufffdcd" (6 bytes); Buffer.from turns the lone surrogate into one U+FFFD.
    r = pig_sdk.truncate_tail("ab\ud800cd", max_bytes=6)
    assert (r.content, r.output_bytes, r.total_bytes, r.last_line_partial) == ("b\ufffdcd", 6, 7, True)


def test_byte_length_counts_a_split_surrogate_pair_as_one_character():
    # Buffer.byteLength("a\ud83d\ude00b") is 6: the pair is one four-byte character even when Python holds it as two code points.
    r = pig_sdk.truncate_head("a\ud83d\ude00b", max_bytes=100)
    assert (r.total_bytes, r.truncated) == (6, False)


def test_negative_limits_match_pi():
    r = pig_sdk.truncate_tail("abc\ndef", max_bytes=-1)
    assert (r.content, r.output_lines, r.output_bytes, r.last_line_partial, r.truncated_by) == ("", 1, 0, True, "bytes")
    # Pi 9e870f6f0: sliceHead keeps nothing for a negative count and never splits a surrogate pair.
    assert pig_sdk.truncate_line("abcdef", -2) == ("... [truncated]", True)
    assert pig_sdk.truncate_line("ab", -5) == ("... [truncated]", True)
    assert pig_sdk.truncate_line("😀😀", -1) == ("... [truncated]", True)
    assert pig_sdk.truncate_line("a😀b", 2) == ("a... [truncated]", True)


def test_tail_over_long_line_followed_by_blank_line():
    # Pi 9e9609216 (#12355): keeps the end of an over-long line followed by blank lines.
    r = pig_sdk.truncate_tail("first\n" + "y" * 20 + "END\n\n", max_bytes=10)
    assert (r.content, r.output_lines, r.output_bytes, r.last_line_partial, r.truncated_by) == ("yyyyyyEND\n", 2, 10, True, "bytes")
    r_nbsp = pig_sdk.truncate_tail("\u00a0\u00a0\u00a0\n\n", max_bytes=5)
    assert (r_nbsp.content, r_nbsp.output_lines, r_nbsp.output_bytes, r_nbsp.last_line_partial, r_nbsp.truncated_by) == ("", 1, 0, False, "bytes")
    r_blank = pig_sdk.truncate_tail("    \n\n", max_bytes=3)
    assert (r_blank.content, r_blank.output_lines, r_blank.output_bytes, r_blank.last_line_partial, r_blank.truncated_by) == ("", 1, 0, False, "bytes")
    r_back = pig_sdk.truncate_tail("x    \n\n", max_bytes=3)
    assert (r_back.content, r_back.output_lines, r_back.output_bytes, r_back.last_line_partial, r_back.truncated_by) == ("  \n", 2, 3, True, "bytes")


if __name__ == "__main__":
    for name, fn in list(globals().items()):
        if name.startswith("test_"):
            fn()
    print("ok")
