"""How a model-issued tool call reaches a tool: prepareArguments runs before the host validates, and a batch starts in source order."""

import threading

import pig_sdk
from test_request_lifetime import connected_extension


def request(req_id, method, tool, args, tool_call_id=None):
    frame = {"method": method, "tool": tool, "args": args}
    if tool_call_id is not None:
        frame["tool_call_id"] = tool_call_id
    return {"type": "request", "id": req_id, "request": frame}


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
    # agent-loop.ts:619-647 and 820-837: the calls of a parallel batch reach tool.execute in source order. The host writes them in source order, so handlers must start in the order the requests arrive.
    batch, rounds = 16, 24
    started = []
    lock = threading.Lock()

    def probe(ctx, args):
        with lock:
            started.append(args["n"])
        return "ok"

    ext = pig_sdk.Extension("py-start-order")
    ext.tool("probe", "Record the start order", {"type": "object"}, probe)
    with connected_extension(ext) as (send, receive):
        for round_number in range(rounds):
            for n in range(batch):
                send(request(f"r{round_number}-{n}", "tool_call", "probe", {"n": n}, "c"))
            for _ in range(batch):
                assert receive("response")["response"]["error"] is None
            with lock:
                got, started[:] = list(started), []
            assert got == list(range(batch)), f"round {round_number}: handlers started as {got}"


def test_a_tool_call_that_ends_without_its_handler_keeps_the_earlier_ones_ahead():
    # agent-loop.ts:619-647: handlers start in source order. A request that ends before its handler hands its place on only after the requests ahead of it began, so a later handler cannot overtake an earlier one.
    order = pig_sdk._ToolStartOrder()
    ahead, skipped, after = order.reserve(), order.reserve(), order.reserve()
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
