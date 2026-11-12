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
    return extension
