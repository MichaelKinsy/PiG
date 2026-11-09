import pig_sdk as sdk


def message_with_block():
    block = {"type": "text", "text": ""}
    return {"role": "assistant", "content": [block], "stopReason": "stop"}, block


def shown_text(event):
    return (event.get("partial") or event["message"])["content"][0]["text"]


def test_events_show_the_message_as_it_was_pushed():
    # The forwarder thread reads the queue after the author went on: Pi's consumer encodes an event before its provider's next await.
    stream = sdk.ModelEventStream()
    message, block = message_with_block()
    stream.push({"type": "start", "partial": message})
    for _ in range(3):
        block["text"] += "x"
        stream.push({"type": "text_delta", "delta": "x", "partial": message})
    block["text"] += "-AFTER"
    stream.push({"type": "done", "reason": "stop", "message": message})
    block["text"] = "changed after done"
    assert [shown_text(event) for event in stream.events()] == ["", "x", "xx", "xxx", "xxx-AFTER"]
    assert stream.result() is message
    assert stream._wire_result()["content"][0]["text"] == "xxx-AFTER"


def test_end_hands_the_host_the_result_as_it_is():
    stream = sdk.ModelEventStream()
    message, block = message_with_block()
    block["text"] = "ended"
    stream.end(message)
    block["text"] = "changed after end"
    assert stream.result() is message
    assert stream._wire_result()["content"][0]["text"] == "ended"


def test_events_the_sdk_builds_are_queued_as_they_are():
    stream = sdk.ModelEventStream()
    event = {"type": "start", "partial": {"content": []}}
    stream._enqueue(event, False)
    stream.end(None)
    assert next(iter(stream.events())) is event
