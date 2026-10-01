"""host-wiring-virtual-model-conflict.py registers a virtual model under the id
of a physical model that host-wiring-session-actions.mjs registers.

Pi flushes the virtual models extensions queued while they loaded after their
provider registrations (agent-session-services.ts:158-193), so the flush fails
with a startup error diagnostic and main.ts exits 1 (main.ts:907-916)."""
import pig_sdk


def _route(ctx, request):
    return {"model": {"provider": "probe", "id": "probe-model-2"}, "thinkingLevel": "off"}


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("host-wiring-virtual-model-conflict")
    ext.register_virtual_model(pig_sdk.VirtualModel(provider="probe", id="probe-model", name="Conflict", route=_route))
    return ext
