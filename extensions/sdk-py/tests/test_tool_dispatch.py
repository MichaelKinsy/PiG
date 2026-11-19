"""How a model-issued tool call reaches a tool: prepareArguments runs before the host validates, and a batch starts in source order."""

import re
import threading

import pig_sdk
from test_request_lifetime import connected_extension


def request(req_id, method, tool, args, tool_call_id=None, batch=None):
    frame = {"method": method, "tool": tool, "args": args}
    if tool_call_id is not None:
        frame["tool_call_id"] = tool_call_id
    if batch is not None:
        frame["batch"] = batch
    return {"type": "request", "id": req_id, "request": frame}


def observe_starts(ext):
    """Record the number of each request (the digits that end its id) as its handler is invoked, where the SDK keeps the start order: after the earlier places were released and before the request's own is. ``take`` returns the numbers recorded since its last call, from the request gate and from the tool batch gate. The order of the handlers' first statements is not what the SDK keeps: a thread that the scheduler holds back after its place was released is overtaken (``test_an_overtaken_handler_keeps_its_invocation_place``)."""
    lock = threading.Lock()
    requests, tools = [], []

    def note(into):
        def observe(method, req_id):
            with lock:
                into.append(int(re.search(r"(\d+)$", req_id).group(1)))

        return observe

    ext._request_starts._observer = note(requests)
    ext._tool_batches._observer = note(tools)

    def take():
        with lock:
            got = list(requests), list(tools)
            requests.clear()
            tools.clear()
        return got

    return take


def test_tool_prepare_arguments_runs_the_hook_for_the_host():
    # agent-loop.ts:707-716 (prepareToolCall) calls tool.prepareArguments before validateToolArguments. The host validates, so it asks the extension for the prepared arguments.
    seen = []

    def prepare(args):
        seen.append(args)
        if args.get("fail"):
            raise RuntimeError("prepare exploded")
        return {"text": args["legacy"]}

    ext = pig_sdk.Extension("py-prepare")
    ext.tool("legacy", "Echo", {"type": "object", "required": ["text"]}, lambda ctx, args: "ok", prepare_arguments=prepare)
    ext.tool("plain", "Plain", {"type": "object"}, lambda ctx, args: "ok")
    with connected_extension(ext) as (send, receive):
        send(request("p1", "tool_prepare_arguments", "legacy", {"legacy": "hello"}))
        assert receive("response")["response"] == {"result": {"text": "hello"}, "error": None}
        send(request("p2", "tool_prepare_arguments", "legacy", {"fail": True}))
        assert receive("response")["response"]["error"] == {"message": "prepare exploded"}
        send(request("p3", "tool_prepare_arguments", "missing", {}))
        assert receive("response")["response"]["error"] is not None
        send(request("p4", "tool_prepare_arguments", "plain", {}))
        assert receive("response")["response"]["error"] is not None
    assert seen == [{"legacy": "hello"}, {"fail": True}]


def test_tool_call_does_not_prepare_the_arguments_again():
    # The host sent the prepared arguments, so preparing them again would apply the hook twice (once in the host's prepareToolCall, once here).
    handled = []
    ext = pig_sdk.Extension("py-prepare-once")
    ext.tool("legacy", "Echo", {"type": "object"}, lambda ctx, args: handled.append(args) or "ok", prepare_arguments=lambda args: {"text": args["legacy"]})
    with connected_extension(ext) as (send, receive):
        send(request("c1", "tool_call", "legacy", {"text": "prepared", "legacy": "raw"}, "tc1"))
        assert receive("response")["response"]["error"] is None
    assert handled == [{"text": "prepared", "legacy": "raw"}]


def test_tool_call_handlers_start_in_arrival_order():
    # agent-loop.ts:619-647 and 820-837: the calls of a parallel batch reach tool.execute in source order. The host writes them in source order and names the batch on each, so handlers of one batch must start in the order the requests arrive.
    batch, rounds = 16, 24
    ran = []
    lock = threading.Lock()

    def probe(ctx, args):
        with lock:
            ran.append(args["n"])
        return "ok"

    ext = pig_sdk.Extension("py-start-order")
    ext.tool("probe", "Record the start order", {"type": "object"}, probe)
    take = observe_starts(ext)
    with connected_extension(ext) as (send, receive):
        for round_number in range(rounds):
            for n in range(batch):
                send(request(f"r{round_number}-{n}", "tool_call", "probe", {"n": n}, "c", batch=f"batch{round_number}"))
            for _ in range(batch):
                assert receive("response")["response"]["error"] is None
            with lock:
                got, ran[:] = sorted(ran), []
            assert got == list(range(batch)), f"round {round_number}: handlers ran as {got}, want each of {list(range(batch))} once"
            requests, tools = take()
            assert requests == list(range(batch)), f"round {round_number}: handlers invoked as {requests} at the request gate"
            assert tools == list(range(batch)), f"round {round_number}: handlers invoked as {tools} at the batch gate"


def test_an_overtaken_handler_keeps_its_invocation_place():
    # The order the SDK keeps is the order of invocation. A handler that the scheduler holds back after its place was released is overtaken by the handlers behind it, so its first statement runs last while it was invoked in its place. The handler is held on a semaphore until every later handler recorded, so the overtaking does not depend on timing.
    batch, held = 18, 6
    ran = []
    lock = threading.Lock()
    recorded = threading.Semaphore(0)

    def probe(ctx, args):
        n = args["n"]
        if n == held:
            for _ in range(batch - 1):
                assert recorded.acquire(timeout=10), "the handlers behind the held one never recorded"
        with lock:
            ran.append(n)
        recorded.release()
        return "ok"

    ext = pig_sdk.Extension("py-start-overtaken")
    ext.tool("probe", "Record the first statement", {"type": "object"}, probe)
    take = observe_starts(ext)
    with connected_extension(ext) as (send, receive):
        for n in range(batch):
            send(request(f"r0-{n}", "tool_call", "probe", {"n": n}, "c", batch="batch0"))
        for _ in range(batch):
            assert receive("response")["response"]["error"] is None
    with lock:
        got = list(ran)
    assert len(got) == batch and got[-1] == held, f"first statements ran as {got}, want {held} last: the held handler was not overtaken"
    requests, tools = take()
    assert requests == list(range(batch)), f"handlers invoked as {requests} at the request gate"
    assert tools == list(range(batch)), f"handlers invoked as {tools} at the batch gate"
    assert sorted(got) == list(range(batch))


def test_a_tool_call_that_ends_without_its_handler_keeps_the_earlier_ones_ahead():
    # agent-loop.ts:619-647: handlers start in source order. A request that ends before its handler hands its place on only after the requests ahead of it began, so a later handler cannot overtake an earlier one.
    order = pig_sdk._RequestStartOrder()
    ahead, skipped, after = order.reserve("tool_call", "r0"), order.reserve("tool_call", "r1"), order.reserve("tool_call", "r2")
    skipped_ended = threading.Event()
    after_began = threading.Event()

    def end_skipped():
        skipped.end()
        skipped.end()
        skipped_ended.set()

    def begin_after():
        after.begin()
        after_began.set()

    threading.Thread(target=end_skipped, daemon=True).start()
    threading.Thread(target=begin_after, daemon=True).start()
    assert not after_began.wait(0.05), "after began before the requests ahead of it"
    assert not skipped_ended.is_set(), "skipped handed its place on before the requests ahead of it"
    ahead.begin()
    assert skipped_ended.wait(5)
    assert after_began.wait(5)


def test_event_and_command_handlers_start_in_arrival_order():
    # runner.ts:1096-1125 and agent-loop.ts:619-647: concurrent emits (the tool_result hooks of a parallel batch) and the calls of a batch start their handlers in call order. The host writes the requests in that order, so handlers must start in the order the requests arrive, whichever the request kind.
    batch, rounds = 18, 24
    ran = []
    lock = threading.Lock()

    def record(n):
        with lock:
            ran.append(n)

    ext = pig_sdk.Extension("py-request-start-order")
    ext.tool("probe", "Record the start order", {"type": "object"}, lambda ctx, args: record(args["n"]) or "ok")
    ext.on_event("tool_result", lambda ctx, data: record(data["n"]))
    ext.command("probe", "Record the start order", lambda ctx, args: record(int(args)))
    take = observe_starts(ext)
    with connected_extension(ext) as (send, receive):
        for round_number in range(rounds):
            for n in range(batch):
                req_id = f"r{round_number}-{n}"
                if n % 3 == 0:
                    send(request(req_id, "tool_call", "probe", {"n": n}, "c"))
                elif n % 3 == 1:
                    send({"type": "request", "id": req_id, "request": {"method": "event", "event": "tool_result", "handler_id": 1, "args": {"n": n}}})
                else:
                    send(request(req_id, "command", "probe", str(n)))
            for _ in range(batch):
                assert receive("response")["response"]["error"] is None
            with lock:
                got, ran[:] = sorted(ran), []
            assert got == list(range(batch)), f"round {round_number}: handlers ran as {got}, want each of {list(range(batch))} once"
            invoked, _ = take()
            assert invoked == list(range(batch)), f"round {round_number}: handlers invoked as {invoked}"


def test_a_request_thread_that_runs_late_does_not_let_a_later_handler_start_first():
    # runner.ts:1096-1125, agent-loop.ts:619-647: concurrent emits and the calls of a batch start their handlers in call order. A scheduler that runs
    # the first request's thread only after the later requests' threads entered must not reorder the handlers: each request holds a place from the
    # arrival of its frame, and a handler is invoked only after the handlers of the earlier requests were.
    started = []
    lock = threading.Lock()

    def record(n):
        with lock:
            started.append(n)

    ext = pig_sdk.Extension("py-late-thread")
    ext.tool("probe", "Record the start order", {"type": "object"}, lambda ctx, args: record(args["n"]) or "ok")
    ext.on_event("tool_result", lambda ctx, data: record(data["n"]))
    ext.command("probe", "Record the start order", lambda ctx, args: record(int(args)))
    entered = {1: threading.Event(), 2: threading.Event()}
    handle = ext._handle_request_thread

    def late_first(env, ctx):
        number = int(env["id"][1:])
        if number == 0:
            assert entered[1].wait(10) and entered[2].wait(10), "the later requests' threads never ran"
        else:
            entered[number].set()
        return handle(env, ctx)

    ext._handle_request_thread = late_first
    take = observe_starts(ext)
    with connected_extension(ext) as (send, receive):
        send(request("r0", "tool_call", "probe", {"n": 0}, "c"))
        send({"type": "request", "id": "r1", "request": {"method": "event", "event": "tool_result", "handler_id": 1, "args": {"n": 1}}})
        send(request("r2", "command", "probe", "2"))
        for _ in range(3):
            assert receive("response")["response"]["error"] is None
    assert sorted(started) == [0, 1, 2]
    invoked, _ = take()
    assert invoked == [0, 1, 2], f"handlers invoked as {invoked}"


def test_a_tool_call_that_reaches_the_batch_gate_late_does_not_let_a_later_call_be_invoked_first():
    # The batch gate follows the request gate, so a request that passed the request gate can still reach the batch gate after the later requests of its batch. The first call is held before the batch gate until the second call reached it: the second call must wait there for the first, and the handlers are invoked in the order of the calls.
    ran = []
    lock = threading.Lock()

    def probe(ctx, args):
        with lock:
            ran.append(args["n"])
        return "ok"

    ext = pig_sdk.Extension("py-late-batch-gate")
    ext.tool("probe", "Record the start order", {"type": "object"}, probe)
    take = observe_starts(ext)
    second_reached = threading.Event()
    reserve = ext._tool_batches.reserve

    def reserve_with_hold(batch, req_id):
        start = reserve(batch, req_id)
        begin = start.begin
        if req_id == "r0":

            def begin_late():
                assert second_reached.wait(10), "the second call never reached the batch gate"
                begin()

            start.begin = begin_late
        elif req_id == "r1":

            def begin_second():
                second_reached.set()
                begin()

            start.begin = begin_second
        return start

    ext._tool_batches.reserve = reserve_with_hold
    with connected_extension(ext) as (send, receive):
        for n in range(3):
            send(request(f"r{n}", "tool_call", "probe", {"n": n}, "c", batch="batch0"))
        for _ in range(3):
            assert receive("response")["response"]["error"] is None
    assert sorted(ran) == [0, 1, 2]
    requests, tools = take()
    assert requests == [0, 1, 2], f"handlers invoked as {requests} at the request gate"
    assert tools == [0, 1, 2], f"handlers invoked as {tools} at the batch gate"
