"""The Python scenario driver: it registers, through the Python SDK's public API,
the extension the scenario Spec describes (sdkscenarios.Spec). The Go, Node and
Rust drivers read the same file and do the same, so one scenario table exercises
every SDK. Keep each driver a straight reading of the Spec: behavior belongs in
the scenario, not here.
"""

import json
import re
import time

import pig_sdk as sdk

def substitute(value, variables):
    """A "${name}" in a string is the answer of the prompt kept as name."""
    if isinstance(value, str):
        return re.sub(r"\$\{(\w+)\}", lambda match: variables.get(match.group(1), ""), value)
    if isinstance(value, list):
        return [substitute(item, variables) for item in value]
    if isinstance(value, dict):
        return {key: substitute(item, variables) for key, item in value.items()}
    return value


def run_flow(flow, interaction, options=None):
    variables = {}
    for step in flow.get("steps") or []:
        if step.get("deviceId"):
            get = options.get_device_id if options else None
            variables[step.get("as", "")] = get() if get else "<none>"
        elif step.get("notify"):
            interaction.notify(substitute(json.loads(step["notify"]), variables))
        else:
            variables[step.get("as", "")] = interaction.prompt(json.loads(step["prompt"]))
    return substitute(json.loads(flow["result"]), variables)


def provider_object(provider):
    model = {"id": provider["id"] + "-model", "name": provider["name"] + " model", "provider": provider["id"], "api": "openai-completions", "baseUrl": "http://127.0.0.1:9", "reasoning": False, "input": ["text"], "cost": {"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100}

    def unused(model, context, options):
        raise RuntimeError("the scenario provider streams nothing")

    def resolve(input):
        key = (input.credential or {}).get("key")
        return sdk.AuthResult({"apiKey": "resolved-" + key}, source="stored credential") if key else None

    api_key = oauth = None
    if provider.get("apiKey"):
        spec = provider["apiKey"]
        login = spec.get("login")
        source = spec.get("checkSource")
        api_key = sdk.APIKeyAuth(spec["name"], resolve, check=(lambda input, source=source: sdk.AuthCheck("api_key", source)) if source else None, login=(lambda interaction, flow=login: run_flow(flow, interaction)) if login else None)
    if provider.get("oauth"):
        spec = provider["oauth"]
        refresh = json.loads(spec["refresh"]) if spec.get("refresh") else {}
        oauth = sdk.OAuthAuth(
            spec["name"],
            login=lambda interaction, options, flow=spec["login"]: run_flow(flow, interaction, options),
            refresh=lambda credential, signal, fields=refresh: {**credential, **fields},
            to_auth=lambda credential: {"apiKey": credential["access"]},
            is_subscription=True if spec.get("subscription") else None,
            login_label=spec.get("loginLabel") or None,
        )
    return sdk.Provider(provider["id"], provider["name"], sdk.ProviderAuth(api_key=api_key, oauth=oauth), lambda: [model], unused, unused)


_recorded = [0]  # the record-counted lines this process wrote


def mark(name):
    """Record that a tool or command started, for the harness to wait on."""
    open(".started-" + name, "wb").close()


def register_tool(extension, tool):
    schema = json.loads(tool["schema"]) if tool.get("schema") else {"type": "object", "properties": {"text": {"type": "string"}}}
    behavior, name = tool["behavior"], tool["name"]

    def run(ctx, params):
        if behavior == "echo":
            return {"content": "echo:" + params.get("text", "")}
        if behavior == "fail":
            raise RuntimeError("boom")
        if behavior == "crash":
            raise KeyError("tool crash")
        if behavior == "details":
            return {"content": "with details", "details": {"count": 2, "nested": {"a": [1, 2]}}}
        if behavior == "soft-error":
            return {"content": "soft failure", "is_error": True}
        if behavior == "bad-text":
            return {"content": [{"type": "text", "text": "a\ud83db"}]}
        if behavior == "empty":
            return {"content": []}
        if behavior == "blocks":
            return {"content": [{"type": "text", "text": "a"}, {"type": "text", "text": "b"}]}
        if behavior == "cooperative":
            mark(name)
            while not ctx.is_cancelled():
                time.sleep(0.01)
            return {"content": "cooperative stopped"}
        if behavior == "hang":
            mark(name)
            while True:
                time.sleep(3600)
        raise RuntimeError("unknown tool behavior " + behavior)

    extension.tool(name, tool["description"], schema, run)


def query(ctx, name):
    if name == "idle":
        return ctx.is_idle()
    if name == "pending":
        return ctx.has_pending_messages()
    if name == "system-prompt":
        return ctx.get_system_prompt()
    if name == "thinking":
        return ctx.get_thinking_level()
    if name == "all-tools":
        return [{"name": tool["name"], "description": tool["description"]} for tool in ctx.get_all_tools()]
    if name == "session-header":
        header = ctx.session_manager.get_header()
        return {"type": header["type"], "id": header["id"], "cwd": header["cwd"]}
    if name == "leaf-entry":
        entry = ctx.session_manager.get_leaf_entry()
        return {"type": entry["type"], "id": entry["id"]}
    if name == "session-manager-id":
        return ctx.session_manager.get_session_id()
    raise RuntimeError("unknown query " + name)


def registry_query(ctx, arg):
    query, provider = arg.split(":")
    registry = ctx.model_registry
    if query == "has-auth":
        return registry.has_configured_auth({"provider": provider, "id": "m"})
    if query == "using-oauth":
        return registry.is_using_oauth({"provider": provider, "id": "m"})
    if query == "provider-name":
        found = registry.get_provider(provider)
        return found.name if found else None
    raise RuntimeError("unknown registry query " + query)


def action(ctx, name):
    if name == "abort":
        ctx.abort()
    elif name == "shutdown":
        ctx.shutdown()
    elif name == "compact":
        ctx.compact(custom_instructions="be brief")
    else:
        raise RuntimeError("unknown action " + name)


_footer_subscribed = []


def append_line(path, line):
    # newline="" keeps "\n" from becoming CRLF on Windows; the Go, Rust and Node fixtures write these bytes.
    with open(path, "a", encoding="utf-8", newline="") as handle:
        handle.write(line + "\n")


def footer_data(ctx, path):
    data = ctx.footer_data
    if not _footer_subscribed:
        _footer_subscribed.append(True)
        data.on_branch_change(lambda: append_line(".branch-changes", str(data.get_git_branch())))
        data.on_branch_change(lambda: append_line(".branch-changes", "unsubscribed-callback-ran"))()
    write(path, json.dumps({"branch": data.get_git_branch(), "statuses": data.get_extension_statuses(), "count": data.get_available_provider_count()}))


def late_register(extension, kind):
    if kind == "command":
        extension.command("late-command", "Registered late", lambda ctx, args: write(args, "ran late"))
    elif kind == "shortcut":
        extension.shortcut("ctrl+alt+l", "Registered late", lambda ctx: write(".shortcut-fired", "late"))
    elif kind == "flag":
        extension.flag("late-flag", "Registered late", "string", "late")
    elif kind == "message-renderer":
        extension.message_renderer("late-type", lambda ctx, message, options, width: [])
    elif kind == "entry-renderer":
        extension.entry_renderer("late-type", lambda ctx, entry, options, width: [])
    else:
        raise ValueError("unknown late registration " + kind)


def register_command(extension, command):
    op, arg = command["op"], command.get("arg", "")

    def run(ctx, path):
        if op == "noop":
            return
        if op == "error":
            raise RuntimeError("command boom")
        if op == "crash":
            raise KeyError("command crash")
        if op == "ping":
            write(path, "pong")
        elif op == "echo":
            write(".echo-args", path)
        elif op == "footer-data":
            footer_data(ctx, path)
        elif op == "late-register":
            late_register(extension, arg)
            write(path, "registered")
        elif op == "registered-provider":
            provider = ctx.model_registry.get_registered_native_provider(arg)
            write(path, provider.name if provider else "none")
        elif op == "flag":
            write(path, json.dumps(ctx.get_flag(arg), separators=(",", ":")))
        elif op == "hang":
            mark(command["name"])
            while True:
                time.sleep(3600)
        elif op == "query":
            write(path, json.dumps(query(ctx, arg), separators=(",", ":")))
        elif op == "signal-wait":
            mark(command["name"])
            ctx.signal.wait()
            write(path, "signal aborted while running")
        elif op == "signal":
            signal = ctx.signal
            write(path, "none" if signal is None else "aborted" if signal.is_set() else "live")
        elif op == "registry":
            write(path, json.dumps(registry_query(ctx, arg), separators=(",", ":")))
        elif op == "action":
            action(ctx, arg)
            write(path, "called")
        elif op == "reload-stale":
            ctx.reload()
            report = "cwd live"
            try:
                ctx.cwd
            except Exception as error:
                report = "cwd threw: " + str(error)
            write(path, report)
        else:
            raise RuntimeError("unknown command op " + op)

    extension.command(command["name"], command["description"], run)


def event_handler(op, tag):
    def run(ctx, data):
        if op == "throw":
            raise RuntimeError("event boom")
        if op == "crash":
            raise KeyError("event crash")
        if op == "block":
            return {"block": True, "reason": "event blocked"}
        if op == "set-content":
            return {"content": [{"type": "text", "text": "patched"}]}
        if op == "set-error":
            return {"isError": True}
        if op == "record":
            append_line(".events", tag + ":" + json.dumps(data, separators=(",", ":"), ensure_ascii=False))
            return None
        if op == "record-counted":
            _recorded[0] += 1
            append_line(".events", f"{tag}@{_recorded[0]}:" + json.dumps(data, separators=(",", ":"), ensure_ascii=False))
            return None
        if op == "set-prompt":
            return {"systemPrompt": "base prompt"}
        if op == "append-prompt":
            return {"systemPrompt": data["systemPrompt"] + "+"}
        if op == "note":
            return {"message": {"customType": "note", "content": "hello", "display": True}}
        if op == "see-prompt":
            return {"message": {"customType": "saw", "content": data["prompt"], "display": False}}
        if op == "transform":
            return {"action": "transform", "text": data["text"] + "!"}
        if op == "handled":
            return {"action": "handled"}
        if op == "continue":
            return {"action": "continue"}
        if op == "see-content":
            content = data.get("content") or [{}]
            return {"details": {"saw": content[0].get("text")}}
        return None

    return run


def write(path, text):
    with open(path, "w", encoding="utf-8", newline="") as handle:
        handle.write(text)


def new_extension():
    # Read at every factory call, so a reload builds the extension from the current file.
    with open(".sdk-scenario.json", encoding="utf-8") as handle:
        SPEC = json.load(handle)
    extension = sdk.Extension("scenario-driver-python")
    for provider in SPEC.get("providers") or []:
        extension.register_native_provider(provider_object(provider))
    for tool in SPEC.get("tools") or []:
        register_tool(extension, tool)
    for command in SPEC.get("commands") or []:
        register_command(extension, command)
    for shortcut in SPEC.get("shortcuts") or []:
        extension.shortcut(shortcut["key"], shortcut["description"], lambda ctx, key=shortcut["key"]: write(".shortcut-fired", key))
    for event in SPEC.get("events") or []:
        extension.on_event(event["name"], event_handler(event["op"], event.get("tag", "")))
    for flag in SPEC.get("flags") or []:
        extension.flag(flag["name"], flag["description"], flag["type"], flag.get("default"))
    return extension
