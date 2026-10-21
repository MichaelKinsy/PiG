"""Every pure pi-ai helper returns what Pi's own pi-ai returned at the pin for each case of test/parity/sdk-vectors/pi-ai/*.json (written by automation/gen/generate-sdk-vectors.mjs)."""
import json
import math
from pathlib import Path

import pytest

import pig_sdk
from pig_sdk import pi_ai_helpers as helpers
from pig_sdk.provider import APIKeyAuthInput, AuthContext, AuthInteraction, ProviderSignal
from pig_sdk.serialize_conversation import _json_stringify

VECTORS = Path(__file__).resolve().parents[3] / "test" / "parity" / "sdk-vectors"


def _is_undefined(value):
    return isinstance(value, dict) and value == {"$": "undefined"}


def _read_vectors(module, export):
    """The cases of test/parity/sdk-vectors/<module>/<export>.json: what Pi's own function returned at the pin."""
    cases = json.loads((VECTORS / module / (export + ".json")).read_text(encoding="utf-8"))["cases"]
    assert cases, export
    return cases


def _load_golden():
    """The pi-ai vectors as the tests below consume them: an argument or answer that is undefined has no value."""
    cases = []
    for path in sorted((VECTORS / "pi-ai").glob("*.json")):
        export = path.stem
        if export in ("envApiKeyAuth", "createAssistantMessageEventStream"):
            continue
        for case in _read_vectors("pi-ai", export):
            entry = {"fn": export, "args": [None if _is_undefined(arg) else arg for arg in case["args"]]}
            if not _is_undefined(case["result"]):
                entry["result"], entry["text"] = case["result"], _json_stringify(case["result"])
            cases.append(entry)
    auth_cases, login = [], None
    for case in _read_vectors("pi-ai", "envApiKeyAuth"):
        name, env_vars, credential, env = case["args"]
        if credential == "$login":
            login = {"prompts": case["result"]["prompts"], "result": case["result"]["result"]}
            continue
        entry = {"name": name, "envVars": env_vars, "env": env, "seen": case["result"]["seen"]}
        if not _is_undefined(credential):
            entry["credential"] = credential
        if case["result"]["result"] is not None:
            entry["result"] = case["result"]["result"]
        auth_cases.append(entry)
    timing = []
    for case in _read_vectors("pi-ai", "createAssistantMessageEventStream"):
        scenario, answer = case["args"][0], case["result"]
        timing.append({**scenario, "present": answer["present"], "value": scenario["preset"] if answer["kept"] else None, "laterTimed": answer["laterTimed"]})
    return {"cases": cases, "auth": {"cases": auth_cases, "login": login}, "timing": timing}


GOLDEN = _load_golden()

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


def test_the_default_retry_delay_cap_is_pis():
    assert helpers.DEFAULT_MAX_AGENT_RETRY_DELAY_MS == 60_000


def test_the_golden_file_covers_every_helper():
    called = {case["fn"] for case in GOLDEN["cases"]}
    assert set(CALLS) <= called


def test_every_helper_matches_the_pinned_package():
    failures = []
    for case in GOLDEN["cases"]:
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
        want = case.get("result")
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


def test_read_stored_credential_matches_the_pinned_package(tmp_path):
    for case in _read_vectors("pi-coding-agent", "readStoredCredential"):
        provider, text = case["args"]
        path = tmp_path / (case["name"].replace(" ", "-") + ".json")
        if not _is_undefined(text):
            path.write_text(text, encoding="utf-8")
        got = pig_sdk.read_stored_credential(provider, str(path))
        assert _json_stringify(got) == _json_stringify(case["result"]["credential"]), case["name"]
