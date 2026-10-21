"""Every pure pi-ai helper returns what Pi's own pi-ai returned at the pin for each call recorded in test/parity/sdk-vectors/pi-ai-helpers/golden.json (written by probe.mjs)."""
import json
import math
from pathlib import Path

import pytest

import pig_sdk
from pig_sdk import pi_ai_helpers as helpers
from pig_sdk.provider import APIKeyAuthInput, AuthContext, AuthInteraction, ProviderSignal
from pig_sdk.serialize_conversation import _json_stringify

GOLDEN = json.loads((Path(__file__).resolve().parents[3] / "test" / "parity" / "sdk-vectors" / "pi-ai-helpers" / "golden.json").read_text(encoding="utf-8"))

CALLS = {
    "StringEnum": helpers.string_enum,
    "getSupportedThinkingLevels": helpers.get_supported_thinking_levels,
    "clampThinkingLevel": helpers.clamp_thinking_level,
    "calculateCost": helpers.calculate_cost,
    "contentText": helpers.content_text,
    "getCurrentTools": helpers.get_current_tools,
    "getCurrentSystemMessage": helpers.get_current_system_message,
    "getCurrentSystemPrompt": helpers.get_current_system_prompt,
    "getInitialSystemMessage": helpers.get_initial_system_message,
    "getDeclaredTools": helpers.get_declared_tools,
    "hasNonAdditiveToolChanges": helpers.has_non_additive_tool_changes,
    "resolveTranscriptTools": helpers.resolve_transcript_tools,
    "collapseSystemMessages": helpers.collapse_system_messages,
    "resolveTranscript": helpers.resolve_transcript,
    "withoutInitialSystemMessage": helpers.without_initial_system_message,
    "normalizeContext": helpers.normalize_context,
    "createInitialSystemMessage": helpers.create_initial_system_message,
    "getSystemMessageText": helpers.get_system_message_text,
    "renderSystemMessageUpdate": helpers.render_system_message_update,
    "isRetryableAssistantError": helpers.is_retryable_assistant_error,
    "isContextOverflow": helpers.is_context_overflow,
    "isRecoverableLength": helpers.is_recoverable_length,
    "retryDelayMs": helpers.retry_delay_ms,
    "getModelType": helpers.get_model_type,
    "isModelType": helpers.is_model_type,
    "hasApi": helpers.has_api,
    "modelsAreEqual": helpers.models_are_equal,
}


def test_the_golden_file_covers_every_helper():
    called = {case["fn"] for case in GOLDEN["cases"]}
    assert set(CALLS) <= called


def test_every_helper_matches_the_pinned_package():
    failures = []
    for case in GOLDEN["cases"]:
        if case["fn"] == "DEFAULT_MAX_AGENT_RETRY_DELAY_MS":
            assert helpers.DEFAULT_MAX_AGENT_RETRY_DELAY_MS == case["result"]
            continue
        got = CALLS[case["fn"]](*json.loads(json.dumps(case["args"])))
        if case["fn"] == "calculateCost":
            got = {key: (None if math.isnan(value) else value) for key, value in got.items()}
        text = "undefined" if got is None and "text" not in case else _json_stringify(got)
        want = case.get("text", "undefined")
        if text != want:
            failures.append((case["fn"], case["args"], text, want))
    assert not failures, failures[:5]


def test_exports_are_on_the_package():
    for name in ("string_enum", "get_supported_thinking_levels", "clamp_thinking_level", "calculate_cost", "content_text", "get_current_tools", "get_current_system_message",
                 "get_current_system_prompt", "normalize_context", "is_retryable_assistant_error", "is_context_overflow", "env_api_key_auth"):
        assert getattr(pig_sdk, name) is getattr(helpers, name)


def test_env_api_key_auth_matches_the_pinned_package():
    for case in GOLDEN["auth"]["cases"]:
        auth = helpers.env_api_key_auth(case["name"], case["envVars"])
        seen = []

        def env(name, case=case, seen=seen):
            seen.append(name)
            return case["env"].get(name)

        result = auth.resolve(APIKeyAuthInput(AuthContext(env, lambda path: False), case.get("credential"), ProviderSignal()))
        got = None
        if result is not None:
            got = {"auth": result.auth, **({"env": result.env} if result.env is not None else {}), "source": result.source}
        want = json.loads(case["text"]) if "text" in case else None
        assert got == want, case
        assert seen == case["seen"], case
    prompts = []
    auth = helpers.env_api_key_auth("Name", ["FIRST", "SECOND"])
    credential = auth.login(AuthInteraction(lambda request: prompts.append(request) or "typed-key", lambda request: None, ProviderSignal()))
    assert prompts == GOLDEN["auth"]["login"]["prompts"] and credential == GOLDEN["auth"]["login"]["result"]
    assert auth.check is None


def test_an_aborted_signal_stops_the_auth():
    signal = ProviderSignal()
    signal.set()
    auth = helpers.env_api_key_auth("Name", ["FIRST"])
    with pytest.raises(RuntimeError):
        auth.resolve(APIKeyAuthInput(AuthContext(lambda name: "x", lambda path: False), None, signal))


def run_timing(case):
    """The golden file's "timing" scenarios: how the final message reaches a stream made by create_assistant_message_event_stream and what it carries."""
    import time

    stream = pig_sdk.create_assistant_message_event_stream()

    def message():
        result = {"role": "assistant", "content": [], "stopReason": "error" if case["kind"] == "error" else "stop"}
        if case["timestamp"] == "now":
            result["timestamp"] = int(time.time() * 1000)
        elif case["timestamp"] == "old":
            result["timestamp"] = 1
        if case["preset"] is not None:
            result["durationMs"] = case["preset"]
        return result

    final, later = message(), message()
    later.pop("durationMs", None)
    later["timestamp"] = int(time.time() * 1000)
    if case["kind"] == "done":
        stream.push({"type": "done", "reason": "stop", "message": final})
    elif case["kind"] == "error":
        stream.push({"type": "error", "reason": "error", "error": final})
    elif case["kind"] == "end":
        stream.end(final)
    else:
        stream.push({"type": "done", "reason": "stop", "message": final})
        stream.end(later)
    return final, later


def test_the_event_stream_times_its_final_message_as_pi_does():
    assert GOLDEN["timing"]
    for case in GOLDEN["timing"]:
        final, later = run_timing(case)
        value = final.get("durationMs")
        assert ("durationMs" in final) == case["present"], case
        assert (case["preset"] if value == case["preset"] and case["preset"] is not None else None) == case["value"], case
        assert ("durationMs" in later) == case["laterTimed"], case
        if "durationMs" in final:
            assert isinstance(value, int) and value >= 0, case


def test_a_stream_the_sdk_makes_for_a_host_operation_is_not_timed():
    stream = pig_sdk.ModelEventStream()
    message = {"role": "assistant", "content": [], "stopReason": "stop", "timestamp": 2**50}
    stream.push({"type": "done", "reason": "stop", "message": message})
    assert "durationMs" not in message
