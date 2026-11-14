import pig_sdk
import threading
import time


def new_extension() -> pig_sdk.Extension:
    extension = pig_sdk.Extension("provider-producer")
    def stream(ctx, model, request, options):
        if options.get("apiKey") != "extension-key":
            raise RuntimeError("wrong key")
        content = request.get("messages", [{}])[-1].get("content")
        if content == "cancel" or (isinstance(content,list) and content and content[0].get("text")=="cancel"):
            ctx.exec("provider-started")
            while not ctx.is_cancelled():
                time.sleep(.001)
            raise RuntimeError("provider stream aborted")
        message = {"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":"custom provider response"}],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}
        result = pig_sdk.ModelEventStream()
        for event in [{"type":"start","partial":message},{"type":"text_start","contentIndex":0,"partial":message},{"type":"text_delta","contentIndex":0,"delta":"custom provider response","partial":message},{"type":"text_end","contentIndex":0,"content":"custom provider response","partial":message},{"type":"done","reason":"stop","message":message}]:
            result.push(event)
        return result
    extension.register_provider("extension-provider",{"api":"issue-8964-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}],"streamSimple":stream})

    def snapshot(ctx, model, request, options):
        # A producer thread appends to one shared partial message and pushes it, without yielding between the two: an event must show the message as it was at the push.
        block = {"type": "text", "text": ""}
        message = {"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[block],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}
        result = pig_sdk.ModelEventStream()
        def produce():
            result.push({"type":"start","partial":message})
            text = ""
            for _ in range(100):
                text += "x"
                block["text"] = text
                result.push({"type":"text_delta","contentIndex":0,"delta":"x","partial":message})
            block["text"] = text + "-AFTER"
            result.push({"type":"done","reason":"stop","message":message})
        threading.Thread(target=produce, daemon=True).start()
        return result
    extension.register_provider("snapshot-provider",{"api":"snapshot-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}],"streamSimple":snapshot})
    def cancel_end(ctx, model, request, options):
        # The producer answers the cancel of its request by ending its stream with a result: it pushes start and three text_delta events, waits until its request is cancelled,
        # and calls end with the message, pushing nothing more.
        import time
        block = {"type": "text", "text": ""}
        message = {"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[block],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}
        result = pig_sdk.ModelEventStream()
        def produce():
            result.push({"type":"start","partial":message})
            for index in range(3):
                block["text"] = "x" * (index + 1)
                result.push({"type":"text_delta","contentIndex":0,"delta":"x","partial":message})
            deadline = time.monotonic() + 10
            while not ctx.is_cancelled() and time.monotonic() < deadline:
                time.sleep(0.005)
            result.end(message)
        threading.Thread(target=produce, daemon=True).start()
        return result
    extension.register_provider("cancel-end-provider",{"api":"cancel-end-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}],"streamSimple":cancel_end})
    def cancel_burst(hold_signal):
        # The producer thread is cancelled in the middle of a burst: it pushes start and three text_delta events, waits until its request is cancelled, and goes on pushing.
        # hold_signal holds the request's signal back for 0.3 s: the SDK's read loop ends the stream of a cancelled request before it sets the signal, and the request's thread
        # then runs between the two. The answer is the abort error in both cases.
        def stream(ctx, model, request, options):
            import time
            if hold_signal:
                set_signal = ctx._cancelled.set
                def set_late():
                    time.sleep(0.3)
                    set_signal()
                ctx._cancelled.set = set_late
            block = {"type": "text", "text": ""}
            message = {"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[block],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}
            result = pig_sdk.ModelEventStream()
            def produce():
                result.push({"type":"start","partial":message})
                for index in range(6):
                    if index == 3:
                        deadline = time.monotonic() + 10
                        while not ctx.is_cancelled() and time.monotonic() < deadline:
                            time.sleep(0.005)
                    block["text"] = "x" * (index + 1)
                    result.push({"type":"text_delta","contentIndex":0,"delta":"x","partial":message})
                result.push({"type":"done","reason":"stop","message":message})
            threading.Thread(target=produce, daemon=True).start()
            return result
        return stream
    extension.register_provider("cancel-burst-provider",{"api":"cancel-burst-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}],"streamSimple":cancel_burst(False)})
    extension.register_provider("cancel-burst-held-provider",{"api":"cancel-burst-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}],"streamSimple":cancel_burst(True)})
    def cancel_error(ctx, model, request, options):
        # The producer answers the abort of its request with an error event that holds its partial message: it pushes start and three text_delta events, waits until its
        # request's signal aborts, and pushes the error event (reason aborted) whose message is the partial with stopReason aborted and its errorMessage.
        import time
        block = {"type": "text", "text": ""}
        message = {"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[block],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}
        result = pig_sdk.ModelEventStream()
        def produce():
            result.push({"type":"start","partial":message})
            for index in range(3):
                block["text"] = "x" * (index + 1)
                result.push({"type":"text_delta","contentIndex":0,"delta":"x","partial":message})
            deadline = time.monotonic() + 10
            while not ctx.is_cancelled() and time.monotonic() < deadline:
                time.sleep(0.005)
            message["stopReason"] = "aborted"
            message["errorMessage"] = "the author saw the abort"
            result.push({"type":"error","reason":"aborted","error":message})
        threading.Thread(target=produce, daemon=True).start()
        return result
    extension.register_provider("cancel-error-provider",{"api":"cancel-error-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}],"streamSimple":cancel_error})
    def cancel_throw(ctx, model, request, options):
        # streamSimple waits until its request's signal aborts and fails, showing nothing.
        import time
        deadline = time.monotonic() + 10
        while not ctx.is_cancelled() and time.monotonic() < deadline:
            time.sleep(0.005)
        raise RuntimeError("the author's stream threw at the abort")
    extension.register_provider("cancel-throw-provider",{"api":"cancel-throw-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}],"streamSimple":cancel_throw})
    def cancel_never(ctx, model, request, options):
        # The author never reacts to the abort of its request: it pushes start and one text_delta whose text is the id of its process, waits until its request's signal
        # aborts, and returns: it pushes nothing more and never ends its stream.
        import os, time
        block = {"type": "text", "text": ""}
        message = {"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[block],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}
        result = pig_sdk.ModelEventStream()
        def produce():
            result.push({"type":"start","partial":message})
            block["text"] = str(os.getpid())
            result.push({"type":"text_delta","contentIndex":0,"delta":block["text"],"partial":message})
            while not ctx.is_cancelled():
                time.sleep(0.005)
        threading.Thread(target=produce, daemon=True).start()
        return result
    extension.register_provider("cancel-never-provider",{"api":"cancel-never-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}],"streamSimple":cancel_never})
    # A provider whose model names an api that no extension registers and that has no streamSimple.
    extension.register_provider("unknown-api-provider",{"api":"unknown-api-no-extension-registered","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":False,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]})
    return extension
