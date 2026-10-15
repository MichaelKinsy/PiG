"""Pi's truncation helpers, file mutation queue and host configuration exports (truncate.ts, file-mutation-queue.ts, config.ts).

The cross-SDK conformance rows run every case against Pi's own code in the Node runtime; these tests keep the documented behavior local.
"""
import json
import os
import threading
import time

import pytest

import pig_sdk
from pig_sdk import file_mutation_queue as fmq


def test_format_size_rounds_half_up_like_to_fixed():
    cases = {0: "0B", 1023: "1023B", 1024: "1.0KB", 1280: "1.3KB", 1536: "1.5KB", 1048575: "1024.0KB", 1048576: "1.0MB", 5505024: "5.3MB"}
    for size, want in cases.items():
        assert pig_sdk.format_size(size) == want


def test_truncate_tail_keeps_the_end_of_an_oversized_last_line():
    result = pig_sdk.truncate_tail("日本語のテキスト", max_bytes=7)
    assert (result.content, result.last_line_partial, result.truncated_by, result.output_bytes) == ("スト", True, "bytes", 6)


def test_truncate_head_never_returns_a_partial_line():
    result = pig_sdk.truncate_head("no newline at all", max_bytes=4)
    assert (result.content, result.first_line_exceeds_limit, result.truncated_by) == ("", True, "bytes")
    result = pig_sdk.truncate_head("a\nb\nc\n", max_lines=2)
    assert (result.content, result.truncated_by, result.total_lines, result.output_lines) == ("a\nb", "lines", 3, 2)


def test_truncate_limits_of_zero_are_used_as_given_and_none_takes_the_default():
    assert pig_sdk.truncate_head("a\nb", max_lines=0).content == ""
    untouched = pig_sdk.truncate_head("a\nb")
    assert (untouched.truncated, untouched.max_lines, untouched.max_bytes) == (False, pig_sdk.DEFAULT_MAX_LINES, pig_sdk.DEFAULT_MAX_BYTES)


def test_truncate_line_counts_utf16_units():
    assert pig_sdk.truncate_line("😀😀😀😀", 6) == ("😀😀😀... [truncated]", True)
    assert pig_sdk.truncate_line("😀😀😀", 6) == ("😀😀😀", False)
    assert pig_sdk.truncate_line("x" * 501)[1] is True and pig_sdk.truncate_line("x" * 500)[1] is False


def _await_admission(key, previous, entered=lambda: False):
    """Wait until a caller started after ``previous`` was observed has taken its queue position, and return its link. ``entered`` ends the wait
    early when the caller is already inside its mutation, which only a queue that does not wait allows."""
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline:
        with fmq._registration:
            current = fmq._chains.get(key)
        if current is not None and current is not previous:
            return current
        if entered():
            return previous
        threading.Event().wait(0.001)
    pytest.fail("a caller never took its queue position")


def test_file_mutation_queue_runs_one_file_at_a_time_in_call_order(tmp_path):
    # Each caller is started only after the previous one was observed in the queue, and the first holds the file until all are queued, so the
    # order and the overlap check depend on the queue, not on thread scheduling.
    target = str(tmp_path / "a.txt")
    key = fmq._queue_key(target)
    release = threading.Event()
    running, order, overlaps = [0], [], []
    lock = threading.Lock()

    def body(number):
        def run():
            with lock:
                running[0] += 1
                if running[0] != 1:
                    overlaps.append(number)
                order.append(number)
            if number == 0:
                release.wait(10)
            with lock:
                running[0] -= 1

        return run

    threads, link = [], None
    for number in range(4):
        thread = threading.Thread(target=pig_sdk.with_file_mutation_queue, args=(target, body(number)), daemon=True)
        threads.append(thread)
        thread.start()
        link = _await_admission(key, link, lambda number=number: number in order)
    release.set()
    for thread in threads:
        thread.join(10)
    assert overlaps == [], "two mutations of one file overlapped"
    assert order == [0, 1, 2, 3]


def test_file_mutation_queue_releases_the_caller_queued_behind_an_error_and_returns_the_result(tmp_path):
    target = str(tmp_path / "a.txt")
    key = fmq._queue_key(target)
    release = threading.Event()
    errors, results = [], []

    def failing():
        release.wait(10)
        raise RuntimeError("boom")

    def first():
        try:
            pig_sdk.with_file_mutation_queue(target, failing)
        except RuntimeError as error:
            errors.append(error)

    holder = threading.Thread(target=first, daemon=True)
    holder.start()
    held = _await_admission(key, None)
    waiter = threading.Thread(target=lambda: results.append(pig_sdk.with_file_mutation_queue(target, lambda: 7)), daemon=True)
    waiter.start()
    _await_admission(key, held)
    release.set()
    holder.join(10)
    waiter.join(10)
    assert len(errors) == 1
    assert results == [7], "the failing mutation never released the caller queued behind it"


def test_file_mutation_queue_fails_without_running_for_an_unresolvable_path(tmp_path):
    loop_a, loop_b = tmp_path / "loop-a", tmp_path / "loop-b"
    os.symlink(loop_b, loop_a)
    os.symlink(loop_a, loop_b)
    ran = []
    with pytest.raises(OSError):
        pig_sdk.with_file_mutation_queue(str(loop_a), lambda: ran.append(1))
    assert ran == []


def _with_host_info(value, check):
    saved = os.environ.get("PIG_EXT_HOST_INFO")
    try:
        if value is None:
            os.environ.pop("PIG_EXT_HOST_INFO", None)
        else:
            os.environ["PIG_EXT_HOST_INFO"] = value
        check()
    finally:
        if saved is None:
            os.environ.pop("PIG_EXT_HOST_INFO", None)
        else:
            os.environ["PIG_EXT_HOST_INFO"] = saved


def test_host_info_reads_the_hosts_environment_entry():
    def check():
        assert pig_sdk.get_agent_dir() == "/home/u/.pig/agent"
        assert pig_sdk.get_config_dir_name() == ".pig"
        assert pig_sdk.get_version() == "1.1.0"

    _with_host_info(json.dumps({"agentDir": "/home/u/.pig/agent", "configDirName": ".pig", "version": "1.1.0"}), check)


@pytest.mark.parametrize("value", [None, "", "{", "[]", json.dumps({"configDirName": ".pig"})])
def test_host_info_is_unavailable_without_a_host_that_provides_it(value):
    def check():
        with pytest.raises(pig_sdk.HostInfoUnavailable):
            pig_sdk.get_agent_dir()

    _with_host_info(value, check)
