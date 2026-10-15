from types import SimpleNamespace

import pig_sdk


def test_provider_superseded_keeps_the_callbacks_the_host_still_calls():
    """Pi keeps one effective registration per provider and merges a later registration's defined values over it, whichever extension makes it (model-runtime.ts:921-940).

    After another extension's partial re-registration, the host's merged registration still calls this extension's stream_simple, operations and OAuth closures, and a retained Provider object keeps its callbacks until provider_release, so provider_superseded drops none of them (TestLateProviderSupersededByAnotherExtensionKeepsOperationsAcrossSDKs runs the cross-process path).
    """
    ext = pig_sdk.Extension("superseded-probe")
    ext._provider_streams = {"gone": lambda *a: None}
    ext._provider_operations = {"gone": {"images": {}}}
    ext._oauth_providers = {"gone": object()}
    ext._native_providers = {"key-gone": SimpleNamespace(id="gone")}

    ext._handle_notify({"type": "notify", "notify": {"method": "provider_superseded", "args": {"name": "gone"}}})

    assert "gone" in ext._provider_streams
    assert "gone" in ext._provider_operations
    assert "gone" in ext._oauth_providers
    assert list(ext._native_providers) == ["key-gone"]
