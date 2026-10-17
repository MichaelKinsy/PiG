"""Pi 1.2.0 runner.ts:1426-1450 renderCurrentSystemPrompt: in a before_agent_start handler, event.systemPrompt and
ctx.getSystemPrompt() render the emission's shared options when read, so the handler sees its own edit at once."""

import pig_sdk
from test_request_lifetime import connected_extension


def test_before_agent_start_prompt_renders_the_current_options():
    ext = pig_sdk.Extension("render-prompt")
    seen = []

    def handler(ctx, data):
        seen.append((data["systemPrompt"], ctx.get_system_prompt()))
        data["systemPromptOptions"]["appendSystemPrompt"] = "EDITED"
        seen.append((data["systemPrompt"], data.get("systemPrompt"), ctx.get_system_prompt()))

    ext.on_event("before_agent_start", handler)
    with connected_extension(ext) as (send, receive):
        args = {"prompt": "hi", "systemPrompt": "RECEIVED", "systemPromptOptions": {"customPrompt": "BASE", "sections": {}}}
        send({"type": "request", "id": "r1", "request": {"method": "event", "event": "before_agent_start", "handler_id": 1, "args": args}})
        call = receive("call")
        assert call["call"]["method"] == "renderSystemPrompt"
        options = call["call"]["args"]["options"]
        assert options["customPrompt"] == "BASE" and options["appendSystemPrompt"] == "EDITED"
        send({"type": "call_result", "id": call["id"], "call_result": {"result": {"prompt": "RENDERED"}}})
        response = receive("response")
        assert response["response"].get("error") is None
        assert response["response"]["result"]["_pigPromptOptions"]["appendSystemPrompt"] == "EDITED"
    # The unedited options read the prompt the host sent without a call; the edit renders once and is then reused.
    assert seen == [("RECEIVED", "RECEIVED"), ("RENDERED", "RENDERED", "RENDERED")]


def test_before_agent_start_prompt_render_error_raises_in_the_handler():
    ext = pig_sdk.Extension("render-prompt-error")
    errors = []

    def handler(ctx, data):
        data["systemPromptOptions"]["sections"]["preamble"] = "x"
        try:
            ctx.get_system_prompt()
        except pig_sdk.HostCallError as exc:
            errors.append(str(exc))

    ext.on_event("before_agent_start", handler)
    with connected_extension(ext) as (send, receive):
        args = {"prompt": "hi", "systemPrompt": "RECEIVED", "systemPromptOptions": {"sections": {}}}
        send({"type": "request", "id": "r1", "request": {"method": "event", "event": "before_agent_start", "handler_id": 1, "args": args}})
        call = receive("call")
        send({"type": "call_result", "id": call["id"], "call_result": {"error": {"message": "invalid section preamble"}}})
        receive("response")
    assert errors == ["invalid section preamble"]
