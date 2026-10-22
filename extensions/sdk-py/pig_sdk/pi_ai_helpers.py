"""pi-ai's pure helpers: StringEnum (utils/typebox-helpers.ts), contentText, getSystemMessageText and renderSystemMessageUpdate (utils/text.ts), the transcript helpers
(utils/transcript.ts), calculateCost, getSupportedThinkingLevels and clampThinkingLevel (models.ts), isContextOverflow and isRecoverableLength (utils/overflow.ts),
isRetryableAssistantError and retryDelayMs (utils/retry.ts), and envApiKeyAuth (auth/helpers.ts).

A model, a message, a context and a tool are the dicts the wire carries. A message or a tool that Pi's types rule out but Pi tolerates (a missing optional field) is read
as Pi reads it; one Pi throws a TypeError on (content that is neither a string nor a list) raises TypeError. The order of a ``sections`` dict is Object.entries order: the
array-index keys first, then the rest in insertion order."""
from __future__ import annotations

import math
import re
from collections.abc import Mapping, Sequence
from typing import Any

from .provider import APIKeyAuth, APIKeyAuthInput, AuthInteraction, AuthResult
from .serialize_conversation import _ABSENT, _content_text, _ordered_keys, _string_or_empty

DEFAULT_MAX_AGENT_RETRY_DELAY_MS = 60_000


def _js_truthy(value: Any) -> bool:
    if value is None or value is _ABSENT:
        return False
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return value != 0 and not (isinstance(value, float) and math.isnan(value))
    if isinstance(value, str):
        return value != ""
    return True


def _number(value: Any) -> float:
    """A JSON number as JavaScript's arithmetic reads it: null is 0, and an absent or non-numeric value is NaN."""
    if value is None:
        return 0.0
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return math.nan
    return float(value)


def _get(obj: Any, key: str) -> Any:
    return obj.get(key, _ABSENT) if isinstance(obj, Mapping) else _ABSENT


def string_enum(values: Sequence[str], options: Mapping[str, Any] | None = None) -> dict[str, Any]:
    """pi-ai's StringEnum: a JSON Schema string enum that Google's API and other providers that reject anyOf/const accept. The keys are type, enum, then description and
    default when options carry a non-empty one."""
    schema: dict[str, Any] = {"type": "string", "enum": list(values)}
    if options is not None and _js_truthy(options.get("description")):
        schema["description"] = options["description"]
    if options is not None and _js_truthy(options.get("default")):
        schema["default"] = options["default"]
    return schema


_EXTENDED_THINKING_LEVELS = ["off", "minimal", "low", "medium", "high", "xhigh", "max"]


def get_supported_thinking_levels(model: Mapping[str, Any]) -> list[str]:
    """pi-ai's getSupportedThinkingLevels: the thinking levels the model accepts, in Pi's order (off, minimal, low, medium, high, xhigh, max). A model without reasoning
    accepts only "off". A level the model's thinkingLevelMap maps to null is not accepted, and xhigh and max are accepted only when the map names them."""
    if not _js_truthy(model.get("reasoning")):
        return ["off"]
    level_map = model.get("thinkingLevelMap")
    if not isinstance(level_map, Mapping):
        level_map = {}
    supported = []
    for level in _EXTENDED_THINKING_LEVELS:
        if level in level_map and level_map[level] is None:
            continue
        if level in ("xhigh", "max") and level not in level_map:
            continue
        supported.append(level)
    return supported


def clamp_thinking_level(model: Mapping[str, Any], level: str) -> str:
    """pi-ai's clampThinkingLevel: level when the model accepts it, else the nearest accepted level at or above it, else the nearest below it, else the first accepted level,
    else "off". A level that is not one of Pi's is the first accepted level."""
    available = get_supported_thinking_levels(model)
    if level in available:
        return level
    first = available[0] if available else "off"
    if level not in _EXTENDED_THINKING_LEVELS:
        return first
    requested = _EXTENDED_THINKING_LEVELS.index(level)
    for candidate in _EXTENDED_THINKING_LEVELS[requested:]:
        if candidate in available:
            return candidate
    for candidate in reversed(_EXTENDED_THINKING_LEVELS[:requested]):
        if candidate in available:
            return candidate
    return first


def calculate_cost(model: Mapping[str, Any], usage: dict[str, Any]) -> dict[str, Any]:
    """pi-ai's calculateCost: it prices usage at the model's per-million-token rates and stores the result in ``usage["cost"]`` (a dict the caller supplies, as in Pi),
    which it also returns. The rates are the model's cost, or those of the tier with the highest inputTokensAbove that the request's input (input, cacheRead and cacheWrite
    tokens) exceeds. Cache writes that ``usage["cacheWrite1h"]`` reports are charged at twice the input rate."""
    cost = usage.get("cost")
    if not isinstance(cost, dict):
        raise TypeError("usage.cost is not an object")
    model_cost = model.get("cost") or {}
    input_tokens = _number(usage.get("input", _ABSENT)) + _number(usage.get("cacheRead", _ABSENT)) + _number(usage.get("cacheWrite", _ABSENT))
    rates = model_cost
    matched = -1.0
    for tier in model_cost.get("tiers") or []:
        above = _number(tier.get("inputTokensAbove", _ABSENT))
        if input_tokens > above and above > matched:
            rates = tier
            matched = above
    # Anthropic charges 2x base input for 1h cache writes.
    long_write = _number(usage.get("cacheWrite1h")) if usage.get("cacheWrite1h") is not None else 0.0
    cache_write = _number(usage.get("cacheWrite", _ABSENT))
    short_write = cache_write - long_write

    def rate(key: str) -> float:
        return _number(rates.get(key, _ABSENT))

    cost["input"] = (rate("input") / 1000000) * _number(usage.get("input", _ABSENT))
    cost["output"] = (rate("output") / 1000000) * _number(usage.get("output", _ABSENT))
    cost["cacheRead"] = (rate("cacheRead") / 1000000) * _number(usage.get("cacheRead", _ABSENT))
    cost["cacheWrite"] = (rate("cacheWrite") * short_write + rate("input") * 2 * long_write) / 1000000
    cost["total"] = cost["input"] + cost["output"] + cost["cacheRead"] + cost["cacheWrite"]
    return cost


def content_text(content: Any, separator: str = "\n") -> str:
    """pi-ai's contentText: a string is its own text, and a list of content blocks is the text of its text blocks joined with separator. Any other content raises
    TypeError, as it throws in Pi."""
    return _content_text(content, separator)


def _is_system(message: Any) -> bool:
    return isinstance(message, Mapping) and message.get("role") == "system"


def _tool_name(tool: Any) -> Any:
    return tool.get("name") if isinstance(tool, Mapping) else None


def _list(value: Any) -> list:
    return value if isinstance(value, list) else []


def get_current_tools(messages: Sequence[Any]) -> list[dict[str, Any]]:
    """pi-ai's getCurrentTools: the tools available after every system message's toolsRemoved and toolsAdded are applied in order, in the order they were first added (a
    tool removed and added again moves to the end)."""
    tools: dict[Any, Any] = {}
    for message in messages:
        if not _is_system(message):
            continue
        for tool in _list(message.get("toolsRemoved")):
            tools.pop(_tool_name(tool), None)
        for tool in _list(message.get("toolsAdded")):
            tools[_tool_name(tool)] = tool
    return list(tools.values())


def _js_ordered(items: dict[str, Any]) -> dict[str, Any]:
    """The dict with its keys in Object.keys order, which is how an object built from a Map reads."""
    return {key: items[key] for key in _ordered_keys(items)}


def get_current_system_message(messages: Sequence[Any]) -> dict[str, Any] | None:
    """pi-ai's getCurrentSystemMessage: every system message replayed into one holding the current prompt and tools. Later content is appended to the base prompt with a blank
    line between, sections are patched by name (None removes one) and tools are resolved as :func:`get_current_tools` does. It is None when the transcript has no system
    message and no tool."""
    content: list[str] = []
    sections: dict[str, Any] = {}
    timestamp: Any = _ABSENT
    for message in messages:
        if not _is_system(message):
            continue
        if timestamp is None or timestamp is _ABSENT:
            timestamp = message.get("timestamp", _ABSENT)
        text = _content_text(message.get("content", _ABSENT), "\n")
        if text:
            content.append(text)
        declared = message.get("sections")
        if isinstance(declared, Mapping):
            for name in _ordered_keys(declared):
                if declared[name] is None:
                    sections.pop(name, None)
                else:
                    sections[name] = declared[name]
    tools = get_current_tools(messages)
    if timestamp is _ABSENT and not tools:
        return None
    result: dict[str, Any] = {"role": "system", "content": "\n\n".join(content)}
    if sections:
        result["sections"] = _js_ordered(sections)
    if tools:
        result["toolsAdded"] = tools
    result["timestamp"] = 0 if timestamp is None or timestamp is _ABSENT else timestamp
    return result


def get_system_message_text(message: Mapping[str, Any]) -> str:
    """pi-ai's getSystemMessageText: a system message rendered as a complete prompt, its content followed by its non-null sections, separated by blank lines."""
    parts = [_content_text(message.get("content", _ABSENT), "\n")]
    sections = message.get("sections")
    if isinstance(sections, Mapping):
        for name in _ordered_keys(sections):
            if sections[name] is not None:
                parts.append(_string_or_empty(sections, name))
    return "\n\n".join(part for part in parts if part)


def get_current_system_prompt(messages: Sequence[Any]) -> str:
    """pi-ai's getCurrentSystemPrompt: the text of :func:`get_current_system_message`, or "" when there is none."""
    message = get_current_system_message(messages)
    return get_system_message_text(message) if message is not None else ""


def render_system_message_update(message: Mapping[str, Any]) -> str:
    """pi-ai's renderSystemMessageUpdate: a later system message rendered for APIs that accept system messages mid-conversation. Section changes are framed by name. Pi
    marks the framing request-time only and subject to change between versions."""
    parts: list[str] = []
    text = _content_text(message.get("content", _ABSENT), "\n")
    if text:
        parts.append(text)
    sections = message.get("sections")
    if isinstance(sections, Mapping):
        for name in _ordered_keys(sections):
            if sections[name] is None:
                parts.append('Removed system prompt section "%s".' % name)
            else:
                parts.append('Updated system prompt section "%s":\n\n%s' % (name, _string_or_empty(sections, name)))
    return "\n\n".join(parts)


def _initial_system_message(system_prompt: Any, tools: Any) -> dict[str, Any] | None:
    has_prompt = isinstance(system_prompt, str) and system_prompt != ""
    tool_list = _list(tools)
    if not has_prompt and not tool_list:
        return None
    message: dict[str, Any] = {"role": "system", "content": system_prompt if isinstance(system_prompt, str) else ""}
    if tool_list:
        message["toolsAdded"] = tool_list
    message["timestamp"] = 0
    return message


def create_initial_system_message(system_prompt: str | None, tools: Sequence[Any] | None) -> dict[str, Any] | None:
    """pi-ai's createInitialSystemMessage: the leading system message for a prompt and tool set, or None when both are empty."""
    return _initial_system_message(system_prompt, list(tools) if tools is not None else None)


def normalize_context(context: Mapping[str, Any]) -> dict[str, Any]:
    """pi-ai's normalizeContext: the context's systemPrompt and tools folded into a leading system message, as the ``{"messages": [...]}`` transcript every provider-facing
    function expects. A context with neither keeps its messages as they are."""
    messages = _list(context.get("messages"))
    initial = _initial_system_message(context.get("systemPrompt"), context.get("tools"))
    return {"messages": [initial, *messages] if initial is not None else messages}


def get_initial_system_message(messages: Sequence[Any]) -> dict[str, Any] | None:
    """pi-ai's getInitialSystemMessage: the leading message when it is a system message, else None."""
    return messages[0] if messages and _is_system(messages[0]) else None


def without_initial_system_message(messages: Sequence[Any]) -> list[Any]:
    """pi-ai's withoutInitialSystemMessage: the messages without a leading system message, for APIs that carry the prompt outside the message list."""
    return list(messages[1:]) if messages and _is_system(messages[0]) else list(messages)


def collapse_system_messages(context: Mapping[str, Any]) -> dict[str, Any]:
    """pi-ai's collapseSystemMessages: the transcript for APIs without mid-conversation system messages. The replayed system message leads and every later system message
    is dropped."""
    messages = _list(context.get("messages"))
    head = get_current_system_message(messages)
    rest = [message for message in messages if not (isinstance(message, Mapping) and message.get("role") == "system")]
    return {"messages": [head, *rest] if head is not None else rest}


def resolve_transcript(context: Mapping[str, Any], supports_mid_convo_system_messages: bool | None) -> Mapping[str, Any]:
    """pi-ai's resolveTranscript: the context itself when the model accepts later system messages, else :func:`collapse_system_messages` of it."""
    return context if supports_mid_convo_system_messages else collapse_system_messages(context)


def get_declared_tools(messages: Sequence[Any]) -> list[Any]:
    """pi-ai's getDeclaredTools: every definition the transcript's toolsAdded names, one per name, in first-declaration order with the latest definition of each."""
    declared: dict[Any, Any] = {}
    for message in messages:
        if _is_system(message):
            for tool in _list(message.get("toolsAdded")):
                declared[_tool_name(tool)] = tool
    return list(declared.values())


def _find_last_non_additive_tool_change(system_messages: Sequence[Any]) -> int:
    """The index of the last system message with a removal or a same-name redeclaration, or -1. A name declared before counts as a redeclaration even after its removal: Kimi K3
    ignores a removed tool that a later tool message declares again."""
    declared: set = set()
    last = -1
    for index, message in enumerate(system_messages):
        if _list(message.get("toolsRemoved")):
            last = index
        for tool in _list(message.get("toolsAdded")):
            name = _tool_name(tool)
            if name in declared:
                last = index
            declared.add(name)
    return last


def has_non_additive_tool_changes(messages: Sequence[Any]) -> bool:
    """pi-ai's hasNonAdditiveToolChanges: whether the tool history holds a removal or a same-name redeclaration that an addition-only transport cannot replay."""
    return _find_last_non_additive_tool_change([message for message in messages if _is_system(message)]) >= 0


def resolve_transcript_tools(messages: Sequence[Any], support: str) -> dict[str, Any]:
    """pi-ai's resolveTranscriptTools: ``{"requestTools": [...], "foldedSystemMessages": int}``, the tool declarations split between the top-level request field and in-place
    changes. ``support`` is pi-ai's TranscriptToolSupport, how a transport can express tool changes after the first request: ``"none"`` (only the top-level tool list exists; it
    always holds the current tools), ``"additions"`` (tools can be added at a later message, but not removed or redefined) or ``"changes"`` (every tool, including the initial
    ones, is declared in place, and a later declaration under the same name replaces the earlier one; removals are not expressible on the wire, so the transport announces them
    as text and the removed tools stay declared). With ``"changes"``, the top-level list stays empty and every system message, including the leading one, declares its tools in
    place. With ``"additions"``, a removal or redefinition cannot be expressed in place, so the top-level list becomes the tool state after the last such change; later additions
    stay anchored, and the cached prefix is invalidated once per removal instead of on every later addition. With ``"none"``, the top-level list is the current tool set. Any
    other value is ``"additions"``, as Pi's comparisons treat it."""
    system_messages = [message for message in messages if _is_system(message)]
    if support == "none":
        return {"requestTools": get_current_tools(system_messages), "foldedSystemMessages": len(system_messages)}
    if support == "changes":
        return {"requestTools": [], "foldedSystemMessages": 0}
    folded = max(1 if get_initial_system_message(messages) is not None else 0, _find_last_non_additive_tool_change(system_messages) + 1)
    return {"requestTools": get_current_tools(system_messages[:folded]), "foldedSystemMessages": folded}


def retry_delay_ms(policy: Mapping[str, Any], attempt: float) -> float:
    """pi-ai's retryDelayMs: the delay before retry attempt (1-indexed): the policy's ``baseDelayMs`` doubled for each earlier attempt, a result that is not a safe integer
    counting as the largest safe integer, capped by ``maxAgentDelayMs`` (60 seconds when the policy names none)."""
    exponent = max(0, attempt - 1)
    try:
        delay = _number(policy.get("baseDelayMs", _ABSENT)) * math.pow(2, exponent)
    except OverflowError:
        delay = _number(policy.get("baseDelayMs", _ABSENT)) * math.inf
    max_safe = 9007199254740991
    safe = delay if math.isfinite(delay) and delay == math.trunc(delay) and abs(delay) <= max_safe else float(max_safe)
    limit = policy.get("maxAgentDelayMs")
    limit = float(DEFAULT_MAX_AGENT_RETRY_DELAY_MS) if limit is None else _number(limit)
    if math.isnan(safe) or math.isnan(limit):
        return math.nan
    return min(safe, limit)


_JS_SPACE = "\\t\\n\\x0b\\x0c\\r \\xa0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000\\ufeff"


def _js_regexp(source: str) -> re.Pattern:
    """Compile the source of a JavaScript regular expression with the i flag (and no u flag) into a pattern that matches the way V8 does on the strings :func:`_to_js_units`
    produces: a letter matches its own two ASCII cases only, in a class too (so ``[a-z0-9]`` holds A-Z), ``.`` is any one UTF-16 unit but a line terminator, ``\\s`` and ``\\S`` are
    JavaScript's white space, ``\\d`` is an ASCII digit, ``\\w`` an ASCII word character and ``$`` only the end of the text. Only the syntax pi-ai's patterns use is supported."""
    out: list[str] = []
    i = 0
    while i < len(source):
        c = source[i]
        if c == "\\":
            i += 1
            n = source[i]
            if n == "d":
                out.append("[0-9]")
            elif n == "w":
                out.append("[0-9A-Za-z_]")
            elif n == "s":
                out.append("[" + _JS_SPACE + "]")
            elif n == "S":
                out.append("[^" + _JS_SPACE + "]")
            else:
                out.append("\\" + n)
        elif c == "[":
            text, i = _js_class(source, i)
            out.append(text)
        elif c == ".":
            out.append("[^\\n\\r\\u2028\\u2029]")
        elif c == "$":
            out.append("\\Z")
        elif c.isascii() and c.isalpha():
            out.append("[" + c.lower() + c.upper() + "]")
        else:
            out.append(c)
        i += 1
    return re.compile("".join(out))


def _js_class(source: str, open_at: int) -> tuple[str, int]:
    """Translate the class that opens at source[open_at] and return it with the index of its closing bracket. The i flag makes a class hold both cases of every ASCII letter it
    names, a range of letters included."""

    def is_letter(c: str) -> bool:
        return c.isascii() and c.isalpha()

    out = ["["]
    i = open_at + 1
    if source[i] == "^":
        out.append("^")
        i += 1
    while source[i] != "]":
        c = source[i]
        if c == "\\":
            i += 1
            n = source[i]
            out.append("0-9" if n == "d" else "0-9A-Za-z_" if n == "w" else "\\" + n)
        elif source[i + 1] == "-" and source[i + 2] != "]":
            high = source[i + 2]
            out.append(c + "-" + high)
            if is_letter(c) and is_letter(high):
                out.append(c.swapcase() + "-" + high.swapcase())
            i += 2
        elif is_letter(c):
            out.append(c.lower() + c.upper())
        else:
            out.append(c)
        i += 1
    out.append("]")
    return "".join(out), i


def _to_js_units(text: str) -> str:
    """The text as one character per UTF-16 code unit, which is what a JavaScript regular expression without the u flag steps over: a character above U+FFFF becomes its two
    surrogate units."""
    if text.isascii():
        return text
    data = text.encode("utf-16-le", "surrogatepass")
    return "".join(chr(int.from_bytes(data[i : i + 2], "little")) for i in range(0, len(data), 2))


def _compile_each(*patterns: str) -> list[re.Pattern]:
    return [_js_regexp(pattern) for pattern in patterns]


_OVERFLOW_PATTERNS = _compile_each(
    r"prompt (?:is )?too long",
    r"prompt exceeds max length",
    r"request_too_large",
    r"input is too long for requested model",
    r"exceeds the context window",
    r"exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))",
    r"input token count.*exceeds the maximum",
    r"maximum prompt length is \d+",
    r"reduce the length of the messages",
    r"maximum context length is \d+ tokens",
    r"exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?",
    r"input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)",
    r"exceeds the limit of \d+",
    r"exceeds the available context size",
    r"greater than the context length",
    r"context window exceeds limit",
    r"exceeded model token limit",
    r"too large for model with \d+ maximum context length",
    r"prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?",
    r"model_context_window_exceeded",
    r"prompt too long; exceeded (?:max )?context length",
    r"range of input length should be",
    r"context[_ ]length[_ ]exceeded",
    r"too many tokens",
    r"token limit exceeded",
    # HTTP 413 means request-size overflow regardless of provider or body text. Match raw SDK errors and formatProviderError's prefix, not a status mentioned in the body.
    r"^(?:[\w -]+ API error \(413\):|413(?:\s|:|$))",
)
_NON_OVERFLOW_PATTERNS = _compile_each(r"^(Throttling error|Service unavailable):", r"rate limit", r"too many requests")
_CEREBRAS_BODYLESS_OVERFLOW_PATTERN = _js_regexp(r"^4(?:00|13)\s*(?:status code)?\s*\(no body\)")
_NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN = _js_regexp("|".join([
    "GoUsageLimitError", "FreeUsageLimitError", "Monthly usage limit reached", "available balance", "insufficient_quota", "out of budget", "quota exceeded", "billing",
    "subscription_sharing_usage_limit_exceeded",
    # Sign in with ChatGPT on a ChatGPT Business plan: a usage limit set by the workspace admin. Only the admin can raise it.
    "subscription_sharing_workplace_individual_usage_limit_exceeded",
]))
_RETRYABLE_PROVIDER_ERROR_PATTERN = _js_regexp("|".join([
    "overloaded", "server_busy", "servers are currently busy", "currently experiencing high demand", "model is at capacity", "rate.?limit", "too many requests",
    # Status codes must not be part of a longer word or number, e.g. a request id or byte count (#11057).
    "(?<![a-z0-9])(?:429|500|502|503|504|520|524)(?![a-z0-9])",
    "service.?(?:is.?)?(?:temporarily.?)?unavailable", "server.?error", "internal.?error", "provider.?returned.?(?:an.?)?error",
    "exceeded request buffer limit while retrying upstream", "unable to forward upstream", "upstream.?request.?failed", "upstream_stream_read_error", "stream was interrupted",
    "http/?2 stream failed", "network.?error", "connection.?error", "connection.?refused", "connection.?lost", "other side closed", "fetch failed",
    r"request to \S+ failed, reason:", "getaddrinfo", "ENOTFOUND", "EAI_AGAIN", "upstream.?connect", "reset before headers", "socket hang up", "socket connection was closed",
    "timed? out", "timeout", "terminated", "websocket.?closed", "websocket.?error", "ended without", "stream ended before message_stop",
    "stream ended before a terminal response event", "http2 request did not get a response",
    # Codex CLI mid-stream disconnect, relayed by proxies that wrap the Codex CLI, e.g. "stream disconnected before completion: stream closed before response.completed" (#11057).
    "stream disconnected before completion",
    "pending stream has been canceled", "retry delay", "you can retry your request",
    "try your request again", "please retry your request", "ResourceExhausted", "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable",
]))


def _assistant_error(message: Mapping[str, Any]) -> tuple[Any, str]:
    error = message.get("errorMessage")
    return message.get("stopReason"), error if isinstance(error, str) else ""


def is_retryable_assistant_error(message: Mapping[str, Any]) -> bool:
    """pi-ai's isRetryableAssistantError: whether a failed assistant message looks like a transient provider or transport error, so the caller can restart the turn. It is a
    classifier, not a retry policy: a quota, billing or subscription-limit message is not retryable. Handle :func:`is_context_overflow` first."""
    stop_reason, error = _assistant_error(message)
    if stop_reason != "error" or not error:
        return False
    text = _to_js_units(error)
    if _NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN.search(text):
        return False
    return _RETRYABLE_PROVIDER_ERROR_PATTERN.search(text) is not None


def is_context_overflow(message: Mapping[str, Any], context_window: float | None = None) -> bool:
    """pi-ai's isContextOverflow: whether an assistant message is a context overflow. It is one when it failed with an overflow error message that is not a rate limit or
    throttling message, or when it failed with a Cerebras body-less 400 or 413. With a truthy context_window it is also one when it stopped normally with more input (input
    and cacheRead tokens) than the window, or stopped at its length limit with no output and input that fills 99% of the window."""
    stop_reason, error = _assistant_error(message)
    if stop_reason == "error" and error:
        text = _to_js_units(error)
        if not any(pattern.search(text) for pattern in _NON_OVERFLOW_PATTERNS):
            if any(pattern.search(text) for pattern in _OVERFLOW_PATTERNS):
                return True
            if message.get("provider") == "cerebras" and _CEREBRAS_BODYLESS_OVERFLOW_PATTERN.search(text):
                return True
    if _js_truthy(context_window):
        usage = message.get("usage") or {}
        input_tokens = _number(usage.get("input", _ABSENT)) + _number(usage.get("cacheRead", _ABSENT))
        if stop_reason == "stop" and input_tokens > context_window:
            return True
        if stop_reason == "length" and _number(usage.get("output", _ABSENT)) == 0 and input_tokens >= context_window * 0.99:
            return True
    return False


def is_recoverable_length(message: Mapping[str, Any], desired_max_output: float) -> bool:
    """pi-ai's isRecoverableLength: whether a length stop ended below the output limit the caller or model intended (desired_max_output, before any context-based clamping),
    so one compact-and-retry attempt may help."""
    usage = message.get("usage") or {}
    return message.get("stopReason") == "length" and desired_max_output > 0 and _number(usage.get("output", _ABSENT)) < desired_max_output


def _throw_if_aborted(signal: Any) -> None:
    if signal is not None and signal.is_set():
        raise RuntimeError("This operation was aborted")


def env_api_key_auth(name: str, env_vars: Sequence[str]) -> APIKeyAuth:
    """pi-ai's envApiKeyAuth: the standard API-key auth of a provider whose key comes from a stored credential or an environment variable. A stored credential's key wins
    (its env travels with it); otherwise the first of env_vars that the auth context finds non-empty resolves, and none resolving is None. Its login prompts for the key with
    a secret prompt and returns an api_key credential. The auth has no check, as in Pi."""
    variables = list(env_vars)

    def login(interaction: AuthInteraction) -> dict[str, Any]:
        _throw_if_aborted(interaction.signal)
        key = interaction.prompt({"type": "secret", "message": "Enter " + name})
        _throw_if_aborted(interaction.signal)
        return {"type": "api_key", "key": key}

    def resolve(auth_input: APIKeyAuthInput) -> AuthResult | None:
        _throw_if_aborted(auth_input.signal)
        credential = auth_input.credential
        if isinstance(credential, Mapping) and _js_truthy(credential.get("key")):
            env = credential.get("env")
            return AuthResult({"apiKey": credential["key"]}, env if isinstance(env, dict) else None, "stored credential")
        for variable in variables:
            value = auth_input.ctx.env(variable)
            _throw_if_aborted(auth_input.signal)
            if value:
                return AuthResult({"apiKey": value}, None, variable)
        return None

    return APIKeyAuth(name=name, resolve=resolve, login=login)


def get_model_type(model: Mapping[str, Any]) -> str:
    """pi-ai's getModelType: the type of a model, "chat", "image" or "classifier". A model without a type is a chat model."""
    kind = model.get("type")
    return "chat" if kind is None else kind


def is_model_type(model: Mapping[str, Any], model_type: str) -> bool:
    """pi-ai's isModelType: whether the model is of the type, a model without a type counting as a chat model."""
    return get_model_type(model) == model_type


def has_api(model: Mapping[str, Any], api: str) -> bool:
    """pi-ai's hasApi: whether the model is a chat model of the API. A model of another type never matches, even when its api names the same API."""
    return is_model_type(model, "chat") and model.get("api") == api


def models_are_equal(a: Mapping[str, Any] | None, b: Mapping[str, Any] | None) -> bool:
    """pi-ai's modelsAreEqual: whether two models have the same type, id and provider. It is False when either is None."""
    if a is None or b is None:
        return False
    return get_model_type(a) == get_model_type(b) and a.get("id") == b.get("id") and a.get("provider") == b.get("provider")
