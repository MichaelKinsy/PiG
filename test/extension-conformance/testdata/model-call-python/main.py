import pig_sdk


def new_extension() -> pig_sdk.Extension:
    extension = pig_sdk.Extension("model-call")

    def call(ctx, args):
        result = ctx.model_registry.complete(
            {"provider": args, "modelId": "m", "api": "openai-completions"},
            {"messages": [{"role": "user", "content": "hello", "timestamp": 1}]},
            {"sessionId": "sdk-session"},
        )
        if result is None or result.get("stopReason") != "stop":
            raise RuntimeError(f"model call = {result}")

    extension.command("model-call", "Make one model call", call)
    return extension
