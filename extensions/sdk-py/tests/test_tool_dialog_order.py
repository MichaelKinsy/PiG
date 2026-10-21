"""The dialog and continuation orders of a tool batch (_ToolStartOrder), mirroring extensions/sdk/tool_dialog_order_test.go.

Pi runs every extension on one thread: two calls of one assistant message that each open a dialog open them in call order, and when the user answers them back to back they resume, and respond, in the order of the answers. The gate tests are deterministic: the order's `parked` hook tells a test each time a caller starts to wait, so nothing sleeps.
"""

import queue
import threading

import pig_sdk
from test_request_lifetime import connected_extension
from test_tool_dispatch import request


class Steps:
    """The names of the steps in the order they ran."""

    def __init__(self):
        self._lock = threading.Lock()
        self._steps = []

    def add(self, step):
        with self._lock:
            self._steps.append(step)

    def list(self):
        with self._lock:
            return list(self._steps)


class Threads:
    def __init__(self):
        self._threads = []

    def go(self, fn):
        thread = threading.Thread(target=fn, daemon=True)
        thread.start()
        self._threads.append(thread)

    def wait(self):
        for thread in self._threads:
            thread.join(5)
            assert not thread.is_alive(), "a gated caller never went on"


def parked_order():
    """An order whose parked hook puts one token on the returned queue each time a caller starts to wait."""
    parked = queue.Queue()
    order = pig_sdk._ToolStartOrder()
    order.parked = lambda: parked.put(None)
    return order, lambda: parked.get(timeout=5)


def test_tool_dialogs_open_in_call_order():
    # The first dialog of each request is written only after the first dialog of every earlier request was written.
    order, wait_parked = parked_order()
    starts = [order.reserve(), order.reserve(), order.reserve()]
    steps, threads = Steps(), Threads()

    def open_dialogs(i):
        def run():
            starts[i].before_call()
            steps.add("abc"[i])
            starts[i].after_call()
            # A second dialog of the same request does not wait again.
            starts[i].before_call()
            starts[i].after_call()
            starts[i].end()

        threads.go(run)

    # The later requests reach their dialogs first: each is held until the one before it opened its own.
    open_dialogs(2)
    wait_parked()
    open_dialogs(1)
    wait_parked()
    assert steps.list() == [], "dialogs opened before the first request's"
    open_dialogs(0)
    threads.wait()
    assert steps.list() == ["a", "b", "c"]


def test_tool_dialog_order_passes_an_ended_request():
    # A request that ends without a dialog hands its place on, also one that failed before its handler ran.
    order, wait_parked = parked_order()
    first, second, third = order.reserve(), order.reserve(), order.reserve()
    steps, threads = Steps(), Threads()

    def open_dialog(name, start):
        def run():
            start.before_call()
            steps.add(name)
            start.after_call()

        threads.go(run)

    open_dialog("third", third)
    wait_parked()
    open_dialog("second", second)
    wait_parked()
    # first ends with no handler and no dialog.
    first.release()
    threads.wait()
    assert steps.list() == ["second", "third"]


def test_ending_a_request_never_waits_for_the_earlier_ones():
    # Passing a dialog place only takes the request out of the queue: a request that ends while an earlier one still holds its place returns at once, and the earlier request's pass readies the next one that remains.
    order, wait_parked = parked_order()
    first, second, third = order.reserve(), order.reserve(), order.reserve()
    steps, threads = Steps(), Threads()

    def open_third():
        third.before_call()
        steps.add("third")
        third.after_call()

    threads.go(open_third)
    wait_parked()
    second.release()  # returns although first still holds its place
    second.release()  # idempotent
    assert steps.list() == [], "third opened before the first request passed its place"
    first.before_call()
    first.after_call()
    threads.wait()
    assert steps.list() == ["third"]


def test_tool_responses_follow_the_order_the_answers_were_read():
    # The response of a handler whose answer the reader read first is written first, though the handler answered second reaches its response first.
    order, wait_parked = parked_order()
    first, second, third = order.reserve(), order.reserve(), order.reserve()
    # The reader reads the answers of second, third and first, in that order.
    second.response_slot().settle()
    third.response_slot().settle()
    first.response_slot().settle()
    steps, threads = Steps(), Threads()

    def respond(name, start):
        threads.go(lambda: start.respond(lambda: steps.add(name)))

    respond("first", first)
    wait_parked()
    respond("third", third)
    wait_parked()
    assert steps.list() == [], "responses were written before the handler answered first"
    respond("second", second)
    threads.wait()
    assert steps.list() == ["second", "third", "first"]


def test_a_handler_that_reached_its_next_host_call_lets_the_ones_behind_it_respond():
    order, wait_parked = parked_order()
    a, b = order.reserve(), order.reserve()
    a.response_slot().settle()
    b.response_slot().settle()
    steps, threads = Steps(), Threads()
    threads.go(lambda: b.respond(lambda: steps.add("b")))
    wait_parked()
    assert steps.list() == []
    a.before_call()  # a's continuation reached its next await
    threads.wait()
    assert steps.list() == ["b"]
    # The next answer settles a again, behind the ones that settled meanwhile.
    a.response_slot().settle()
    c = order.reserve()
    c.response_slot().settle()
    a.respond(lambda: steps.add("a"))
    c.respond(lambda: steps.add("c"))
    assert steps.list() == ["b", "a", "c"]


def test_a_handler_that_settled_no_slot_responds_at_once():
    order, _ = parked_order()
    a, b = order.reserve(), order.reserve()
    a.response_slot().settle()
    steps = Steps()
    b.respond(lambda: steps.add("b"))
    assert steps.list() == ["b"], "an unsettled handler waited for a settled one"


def test_a_request_that_ends_closes_its_slot():
    # A settled handler that never responds (its request ended) stops holding up the ones behind it.
    order, wait_parked = parked_order()
    a, b = order.reserve(), order.reserve()
    a.response_slot().settle()
    b.response_slot().settle()
    steps, threads = Steps(), Threads()
    threads.go(lambda: b.respond(lambda: steps.add("b")))
    wait_parked()
    a.release()
    threads.wait()
    assert steps.list() == ["b"]
    # A closed slot settles no more.
    assert a.response_slot() is None
    c = order.reserve()
    c.respond(lambda: steps.add("c"))
    assert steps.list() == ["b", "c"]


def test_a_failed_response_still_closes_the_slot():
    order, wait_parked = parked_order()
    a, b = order.reserve(), order.reserve()
    a.response_slot().settle()
    b.response_slot().settle()
    steps, threads = Steps(), Threads()
    threads.go(lambda: b.respond(lambda: steps.add("b")))
    wait_parked()

    def fail():
        raise OSError("socket closed")

    try:
        a.respond(fail)
    except OSError:
        pass
    threads.wait()
    assert steps.list() == ["b"]


def test_a_retained_context_after_the_request_ended_neither_waits_nor_gets_a_slot():
    # A dialog a retained context opens once its request ended passes no place and waits for none, and it leaves no slot that nothing would close: a slot settled after the end would hold every later response.
    order, _ = parked_order()
    ended, held = order.reserve(), order.reserve()
    ended.release()
    ended.before_call()  # returns at once: the place passed with the request
    assert ended.response_slot() is None
    ended.after_call()
    held.response_slot().settle()
    steps = Steps()
    held.respond(lambda: steps.add("held"))
    assert steps.list() == ["held"]


def test_a_handler_that_opens_no_dialog_has_no_slot_and_responds_at_once():
    order, _ = parked_order()
    plain = order.reserve()
    plain.before_call()
    plain.after_call()
    assert plain._slot is None
    steps = Steps()
    plain.respond(lambda: steps.add("plain"))
    assert steps.list() == ["plain"]


def _frame_text(frame):
    return frame["call"]["args"]["title"]


def test_dispatch_dialogs_open_in_call_order_and_responses_follow_the_answers():
    # A fake host writes two tool_call requests, answers the second dialog first and the first second, back to back, and reads the dialogs' call order and the responses' order off the socket.
    ext = pig_sdk.Extension("py-dialog-order")

    def ask(ctx, args):
        selected, ok = ctx.select(args["name"], ["x"])
        assert ok
        return f"{args['name']}:{selected}"

    ext.tool("ask", "Open a dialog", {"type": "object"}, ask)
    with connected_extension(ext) as (send, receive):
        send(request("r1", "tool_call", "ask", {"name": "a"}, "tc1", batch="batch"))
        send(request("r2", "tool_call", "ask", {"name": "b"}, "tc2", batch="batch"))
        calls = [receive("call"), receive("call")]
        assert [call["call"]["method"] for call in calls] == ["ui.select", "ui.select"]
        assert [_frame_text(call) for call in calls] == ["a", "b"], "the dialogs did not open in call order"
        assert [call["call"]["parent_request_id"] for call in calls] == ["r1", "r2"]
        for call in reversed(calls):
            send({"type": "call_result", "id": call["id"], "call_result": {"result": {"ok": True, "selected": "x"}}})
        responses = [receive("response"), receive("response")]
        assert [response["id"] for response in responses] == ["r2", "r1"], "responses did not follow the order of the answers"
        assert [response["response"]["error"] for response in responses] == [None, None]


def test_orders_are_per_batch_and_dropped_when_the_batch_ended():
    batches = pig_sdk._ToolBatches()
    a1, a2 = batches.reserve("A"), batches.reserve("A")
    b1 = batches.reserve("B")
    assert a1._order is a2._order and a1._order is not b1._order
    # B's first dialog does not wait for A's requests, which still run.
    b1.before_call()
    b1.after_call()
    order = a1._order
    a1.end()
    assert batches._orders["A"] is order
    a2.end()
    assert "A" not in batches._orders
    b1.end()
    b1.end()  # idempotent: the count of live requests drops once
    assert batches._orders == {}
    # A request that arrives after every earlier one of its batch ended finds a fresh order.
    assert batches.reserve("A")._order is not order


def test_dispatch_batches_and_requests_without_a_batch_do_not_gate_each_other():
    # A long request without a dialog in batch A must not hold the first dialog of batch B, or of a request outside any batch.
    ext = pig_sdk.Extension("py-dialog-batches")
    release_a = threading.Event()

    def block(ctx, args):
        assert release_a.wait(5), "the long request was never released"
        return "long"

    def ask(ctx, args):
        selected, ok = ctx.select(args["name"], ["x"])
        return f"{args['name']}:{selected}"

    ext.tool("block", "A long request without a dialog", {"type": "object"}, block)
    ext.tool("ask", "Open a dialog", {"type": "object"}, ask)
    with connected_extension(ext) as (send, receive):
        try:
            send(request("a1", "tool_call", "block", {}, "tc1", batch="A"))
            send(request("b1", "tool_call", "ask", {"name": "b"}, "tc2", batch="B"))
            send(request("n1", "tool_call", "ask", {"name": "n"}, "tc3"))
            calls = [receive("call"), receive("call")]
            assert sorted(_frame_text(call) for call in calls) == ["b", "n"], "a dialog was held by another batch's long request"
            for call in calls:
                send({"type": "call_result", "id": call["id"], "call_result": {"result": {"ok": True, "selected": "x"}}})
            answered = {receive("response")["id"] for _ in range(2)}
            assert answered == {"b1", "n1"}
        finally:
            release_a.set()
        assert receive("response")["id"] == "a1"


def test_the_first_host_call_of_any_kind_passes_the_dialog_place():
    # Pi's handler that awaits an exec before its first dialog has yielded the thread, so the later calls' dialogs open while its exec runs: the first host call, whatever it is, passes the place on.
    order, _ = parked_order()
    first, second, third = order.reserve(), order.reserve(), order.reserve()
    held = []

    def parked():
        held.append(None)
        first.release()  # lets the held call return, so the test fails instead of hanging

    order.parked = parked
    first.before_call()
    first.after_call()  # the exec is written; the handler goes on without ending
    second.before_call()
    second.after_call()
    third.before_call()
    third.after_call()
    first.before_call()  # first's own dialog, after its exec, does not wait again
    first.after_call()
    assert held == [], "a later call waited for the dialog place of a request whose exec is in flight"
