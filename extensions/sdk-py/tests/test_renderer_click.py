"""Message and entry renderers keep per-message state and take mouse events (Pi's custom message renderer: a WeakMap keyed by the message and a MouseRegion click that toggles it)."""

import pig_sdk
from pig_sdk import kit
from test_request_lifetime import connected_extension


def mouse(node="card", x=3, y=1, key="k"):
    return {"key": key, "node": node, "type": "mouse", "index": 0, "mouse": {"type": "click", "button": "left", "x": x, "y": y}}


def render_request(req_id, method, custom_type, subject_key, subject, **args):
    args = {subject_key: subject, "options": {"expanded": False, "outputPad": 2}, "width": 40, **args}
    return {"type": "request", "id": req_id, "request": {"method": method, "tool": custom_type, "args": args}}


def text(response):
    assert response["response"]["error"] is None, response
    return response["response"]["result"]["view"]["root"]["text"]


def counting_extension(seen):
    """A message and an entry view renderer that count the clicks of their card in the options' state."""
    ext = pig_sdk.Extension("py-render-click")

    def handle(ctx, subject, options, event):
        seen.append((subject, options.state, event))
        options.state["clicks"] = options.state.get("clicks", 0) + 1
        options.state["last"] = f"{event.mouse['x']},{event.mouse['y']}"

    def view(ctx, subject, options, width):
        seen.append((subject, options.state, None))
        return kit.View(kit.Text(f"{subject.get('content', subject.get('data'))}:{options.state.get('clicks', 0)}:{options.state.get('last', 'none')}", 0, 0))

    ext.message_view_renderer("card", view)
    ext.message_view_event("card", handle)
    ext.entry_view_renderer("mark", view)
    ext.entry_view_event("mark", handle)
    return ext


def test_events_run_the_handler_before_the_render_sees_the_state():
    seen = []
    with connected_extension(counting_extension(seen)) as (send, receive):
        send(render_request("1", "render_message", "card", "message", {"content": "hi"}, key="m1", events=[mouse(x=3, y=1), mouse(x=5, y=2)]))
        assert text(receive("response")) == "hi:2:5,2"
        assert [event.mouse["x"] for _, _, event in seen if event is not None] == [3, 5]
        assert seen[-1][2] is None, "the render ran after both events"
        send(render_request("2", "render_entry", "mark", "entry", {"data": "e"}, key="e1", events=[mouse(x=7, y=0)]))
        assert text(receive("response")) == "e:1:7,0"


def test_a_key_keeps_its_first_subject_and_its_state_across_requests():
    seen = []
    with connected_extension(counting_extension(seen)) as (send, receive):
        send(render_request("1", "render_message", "card", "message", {"content": "first"}, key="m1", events=[mouse(x=1, y=1)]))
        assert text(receive("response")) == "first:1:1,1"
        send(render_request("2", "render_message", "card", "message", {"content": "second"}, key="m1"))
        assert text(receive("response")) == "first:1:1,1", "the key's first decoded subject and state"
        subjects = {id(subject) for subject, _, _ in seen}
        states = {id(state) for _, state, _ in seen}
        assert len(subjects) == 1 and len(states) == 1
        assert seen[-1][0] == {"content": "first"}


def test_another_key_is_independent():
    seen = []
    with connected_extension(counting_extension(seen)) as (send, receive):
        send(render_request("1", "render_message", "card", "message", {"content": "a"}, key="m1", events=[mouse()]))
        assert text(receive("response")) == "a:1:3,1"
        send(render_request("2", "render_message", "card", "message", {"content": "b"}, key="m2"))
        assert text(receive("response")) == "b:0:none"
        send(render_request("3", "render_entry", "mark", "entry", {"data": "c"}, key="e1"))
        assert text(receive("response")) == "c:0:none"


def test_render_release_forgets_the_state():
    seen = []
    with connected_extension(counting_extension(seen)) as (send, receive):
        send(render_request("1", "render_message", "card", "message", {"content": "a"}, key="m1", events=[mouse()]))
        assert text(receive("response")) == "a:1:3,1"
        send({"type": "notify", "notify": {"method": "render_release", "args": {"key": "m1"}}})
        # The notify and the request arrive in order on one connection; a new request for the key starts a new card.
        send(render_request("2", "render_message", "card", "message", {"content": "again"}, key="m1"))
        assert text(receive("response")) == "again:0:none"


def test_a_request_without_a_key_keeps_nothing():
    seen = []
    with connected_extension(counting_extension(seen)) as (send, receive):
        send(render_request("1", "render_message", "card", "message", {"content": "a"}, events=[mouse()]))
        assert text(receive("response")) == "a:1:3,1"
        send(render_request("2", "render_message", "card", "message", {"content": "b"}, key=""))
        assert text(receive("response")) == "b:0:none"
        send(render_request("3", "render_message", "card", "message", {"content": "c"}))
        assert text(receive("response")) == "c:0:none"
        assert len({id(state) for _, state, _ in seen}) == 3, "each call gets a card of its own"


def test_events_without_a_handler_are_ignored():
    ext = pig_sdk.Extension("py-render-no-handler")
    ext.message_view_renderer("plain", lambda ctx, message, options, width: kit.View(kit.Text(f"{message['content']}:{sorted(options.state)}", 0, 0)))
    with connected_extension(ext) as (send, receive):
        send(render_request("1", "render_message", "plain", "message", {"content": "x"}, key="m1", events=[mouse()]))
        assert text(receive("response")) == "x:[]"


def test_an_unknown_event_type_is_ignored():
    seen = []
    with connected_extension(counting_extension(seen)) as (send, receive):
        send(render_request("1", "render_message", "card", "message", {"content": "x"}, key="m1", events=[{"key": "k", "node": "card", "type": "nonsense"}, mouse()]))
        assert text(receive("response")) == "x:1:3,1"


def test_a_failing_handler_does_not_stop_later_events_or_the_render():
    rendered = []
    ext = pig_sdk.Extension("py-render-failing")

    def handle(ctx, subject, options, event):
        options.state["n"] = options.state.get("n", 0) + 1
        if event.mouse["x"] == 1:
            raise RuntimeError("click exploded")

    def view(ctx, subject, options, width):
        rendered.append(options.state["n"])
        return kit.View(kit.Text("v", 0, 0))

    ext.message_view_renderer("card", view)
    ext.message_view_event("card", handle)
    with connected_extension(ext) as (send, receive):
        send(render_request("1", "render_message", "card", "message", {"content": "x"}, key="m1", events=[mouse(x=1), mouse(x=2)]))
        answer = receive("response")
        assert answer["response"]["error"] == {"message": "click exploded"}
        assert rendered == [2], "both events applied and the render ran"
        send(render_request("2", "render_message", "card", "message", {"content": "x"}, key="m1"))
        assert text(receive("response")) == "v"
        assert rendered == [2, 2]


def test_a_failing_render_wins_over_a_failing_handler():
    ext = pig_sdk.Extension("py-render-failing-render")

    def view(ctx, subject, options, width):
        raise RuntimeError("render exploded")

    def handle(ctx, subject, options, event):
        raise RuntimeError("click exploded")

    ext.entry_view_renderer("mark", view)
    ext.entry_view_event("mark", handle)
    with connected_extension(ext) as (send, receive):
        send(render_request("1", "render_entry", "mark", "entry", {"data": "x"}, key="e1", events=[mouse()]))
        assert receive("response")["response"]["error"] == {"message": "render exploded"}


def test_line_renderers_get_the_state_too():
    states = []
    ext = pig_sdk.Extension("py-render-lines")

    def lines(ctx, message, options, width):
        states.append(options.state)
        options.state["n"] = options.state.get("n", 0) + 1
        return [f"n={options.state['n']}"]

    ext.message_renderer("note", lines)
    with connected_extension(ext) as (send, receive):
        send(render_request("1", "render_message", "note", "message", {"content": "x"}, key="m1"))
        assert receive("response")["response"]["result"] == {"lines": ["n=1"]}
        send(render_request("2", "render_message", "note", "message", {"content": "x"}, key="m1"))
        assert receive("response")["response"]["result"] == {"lines": ["n=2"]}
        assert states[0] is states[1]


def test_the_state_is_an_attribute_and_not_a_key_of_the_options():
    options = pig_sdk.MessageRenderOptions({"expanded": True})
    assert options == {"expanded": True}
    assert options.state == {}
    shared = {"clicks": 1}
    assert pig_sdk.EntryRenderOptions({}, shared).state is shared


def test_registering_an_event_handler_declares_nothing_to_the_host():
    ext = pig_sdk.Extension("py-render-declaration")
    ext.message_view_renderer("card", lambda ctx, message, options, width: kit.View(kit.Text("x", 0, 0)))
    declared = list(ext._renderers)
    ext.message_view_event("card", lambda ctx, message, options, event: None)
    ext.entry_view_event("mark", lambda ctx, entry, options, event: None)
    assert ext._renderers == declared
    assert ext._entry_renderers == []
