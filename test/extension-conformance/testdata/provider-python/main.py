import pig_sdk
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
    return extension
