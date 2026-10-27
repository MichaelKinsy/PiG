import pig_sdk


def new_extension() -> pig_sdk.Extension:
    extension = pig_sdk.Extension("provider-diag")

    def model(model_id):
        return [{"id": model_id, "name": model_id, "reasoning": False, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4096, "maxTokens": 1024}]

    extension.register_provider("diag-good", {"api": "openai-completions", "baseUrl": "https://good.invalid/v1", "apiKey": "key", "models": model("good-model")})
    extension.register_provider("diag-bad", {"baseUrl": "https://bad.invalid/v1", "apiKey": "key", "models": model("bad-model")})
    extension.register_tool(pig_sdk.ToolDefinition(name="diag_ping", label="diag_ping", description="diag_ping", parameters={"type": "object"}, execute=lambda ctx, args: "pong"))
    return extension
