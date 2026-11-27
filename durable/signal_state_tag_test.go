package durable

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Pi 32c71a6d4 tags SignalState with kind: "value" | "expired" while keeping stored signal memos unchanged.
func TestSignalStateIsTaggedAndMemosUnchanged(t *testing.T) {
	typ := reflect.TypeFor[SignalState[string]]()
	field, ok := typ.FieldByName("Kind")
	if !ok {
		t.Fatalf("SignalState has no Kind field (fields: %d); want tagged union with Kind", typ.NumField())
	}
	if field.Type.Kind() != reflect.String {
		t.Fatalf("SignalState.Kind has underlying kind %v, want string", field.Type.Kind())
	}
	if _, hasExpired := typ.FieldByName("Expired"); hasExpired {
		t.Fatalf("SignalState still has Expired bool field; Pi 32c71a6d4 replaced it with Kind == expired")
	}
	token := DefineSignal(SignalDefinition[string]{Name: "test.sig", Version: 1, Schema: map[string]any{"type": "string"}})
	memos := map[string]JsonValue{
		"pi.signal.test.sig/v": delta.JsonObjectOf("version", float64(1), "value", "hello"),
		"pi.signal.test.sig/e": delta.JsonObjectOf("expired", true),
	}
	valState, err := ReadSignalMemo(memos, token, "v")
	if err != nil || valState == nil {
		t.Fatalf("ReadSignalMemo(v) = %+v, %v", valState, err)
	}
	expState, err := ReadSignalMemo(memos, token, "e")
	if err != nil || expState == nil {
		t.Fatalf("ReadSignalMemo(e) = %+v, %v", expState, err)
	}
	valJSON, err := json.Marshal(valState)
	if err != nil {
		t.Fatal(err)
	}
	expJSON, err := json.Marshal(expState)
	if err != nil {
		t.Fatal(err)
	}
	if string(valJSON) != `{"kind":"value","value":"hello"}` {
		t.Fatalf("value SignalState json = %s, want {\"kind\":\"value\",\"value\":\"hello\"}", valJSON)
	}
	if string(expJSON) != `{"kind":"expired"}` {
		t.Fatalf("expired SignalState json = %s, want {\"kind\":\"expired\"}", expJSON)
	}
}
