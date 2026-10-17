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
    assert pig_sdk.truncate_line("abcdef", -2) == ("abcd... [truncated]", True)
    assert pig_sdk.truncate_line("ab", -5) == ("... [truncated]", True)
    assert pig_sdk.truncate_line("😀😀", -1) == ("😀\ud83d... [truncated]", True)


if __name__ == "__main__":
    for name, fn in list(globals().items()):
        if name.startswith("test_"):
            fn()
    print("ok")
