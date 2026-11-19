package sdk

import "testing"

// pig additive (D273), the stated limit: a key a handler deleted from an event's object and set again keeps the place the host gave it in ObjectKeys. In Pi the
// object is a JavaScript object, where Object.keys puts a deleted and re-set key last (["model","messages","zeta","stream"] here), so this pins PiG's order, not Pi's.
func TestObjectKeysKeepTheOldPlaceOfAKeyDeletedAndSetAgain(t *testing.T) {
	ext := New("key-order-reset")
	ext.OnEvent(EventBeforeProviderRequest, func(ctx Context, data map[string]any) (any, error) {
		payload := data["payload"].(map[string]any)
		delete(payload, "stream")
		payload["stream"] = false
		return map[string]any{"keys": ctx.ObjectKeys(payload)}, nil
	})
	dispatch := keyOrderFixture(t, ext)
	got := dispatch(1, EventBeforeProviderRequest, `{"type":"before_provider_request","payload":{"model":"m","messages":[],"stream":true,"zeta":{"z":1,"a":2}}}`)
	const want = `{"keys":["model","messages","stream","zeta"]}`
	if got != want {
		t.Errorf("keys\n got %s\nwant %s (Pi's Object.keys would give model, messages, zeta, stream)", got, want)
	}
}
