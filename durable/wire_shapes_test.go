package durable

// pi: packages/durable/src/documents.ts

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// A base always carries its value and a delta its ops, even when empty: the JSONL storage recovers an unconfirmed
// creation as { kind: "base", version: 1, value: {} } (storage/jsonl/storage.ts:697) and stores content with
// JSON.stringify, which keeps an empty object or array.
func TestDocumentContentKeepsAnEmptyValueOrOps(t *testing.T) {
	base := DocumentContent{Version: 1, Kind: ContentBase, Value: delta.NewJsonObject(0)}
	if got := jsonOf(t, base); got != `{"version":1,"kind":"base","value":{}}` {
		t.Fatalf("base = %s", got)
	}
	delta := DocumentContent{Version: 2, Kind: ContentDelta}
	if got := jsonOf(t, delta); got != `{"version":2,"kind":"delta","ops":[]}` {
		t.Fatalf("delta = %s", got)
	}
	var decoded DocumentContent
	if err := json.Unmarshal([]byte(`{"kind":"base","version":1,"value":{}}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Value == nil || decoded.Value.Len() != 0 || decoded.Kind != ContentBase || decoded.Version != 1 {
		t.Fatalf("decoded = %#v", decoded)
	}
}

// A waiting state always carries on and policy: generation waits on `on: tools` (harness/generation.ts:585), which is
// empty when every call of the round settled inline, and the scheduler reads state.on unconditionally
// (harness/scheduler.ts:389, :465).
func TestWaitingTaskStateKeepsAnEmptyOn(t *testing.T) {
	var checkpoint JsonValue = map[string]any{"phase": "tools"}
	state := TaskState[JsonValue, JsonValue]{Status: TaskWaiting, Checkpoint: &checkpoint, Policy: JoinAllSettled}
	if got := jsonOf(t, state); got != `{"status":"waiting","checkpoint":{"phase":"tools"},"on":[],"policy":"allSettled"}` {
		t.Fatalf("waiting = %s", got)
	}
	running := TaskState[JsonValue, JsonValue]{Status: TaskRunning, Checkpoint: &checkpoint}
	if got := jsonOf(t, running); got != `{"status":"running","checkpoint":{"phase":"tools"}}` {
		t.Fatalf("running = %s", got)
	}
}

// resolveAddress reads the family key as `args[index++] as string`; an absent key leaves the address without a key,
// so addressId encodes null (documents.ts resolveAddress, addressId).
func TestResolveAddressKeepsAnAbsentFamilyKeyAbsent(t *testing.T) {
	family := DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{
		Family: true,
		Kind:   "notes", Version: 1,
		DocumentSemantics: DocumentSemantics{Scope: ScopeSession},
		Initial:           func(JsonValue) JsonObject { return delta.NewJsonObject(0) },
	})
	resolved, err := ResolveAddress(family.AnyDefinition(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Address.Key != nil || resolved.Id != `["notes","session",null,null]` || resolved.NextArgument != 1 {
		t.Fatalf("resolved = %#v", resolved)
	}
	keyed, err := ResolveAddress(family.AnyDefinition(), []any{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if keyed.Address.Key == nil || *keyed.Address.Key != "a" || keyed.Id != `["notes","session",null,"a"]` {
		t.Fatalf("keyed = %#v", keyed)
	}
}

// ownerId rejects a number that is not a safe integer (documents.ts ownerId: Number.isSafeInteger).
func TestResolveAddressRejectsAnUnsafeOwnerId(t *testing.T) {
	token := DefineDoc(DocDefinition[JsonObject]{
		CommonDocDefinition: CommonDocDefinition[JsonObject]{Kind: "plan", Version: 1, Initial: func() JsonObject { return delta.NewJsonObject(0) }},
		DocumentSemantics:   DocumentSemantics{Scope: ScopeConversation, History: HistoryLatest, Fork: ForkCurrent},
	})
	for _, owner := range []any{ConversationId(1 << 53), int64(-(1 << 53)), 1 << 60} {
		if _, err := ResolveAddress(token.AnyDefinition(), []any{owner}); err == nil || err.Error() != "Document plan requires a conversation ID" {
			t.Fatalf("owner %v: err = %v", owner, err)
		}
	}
	if resolved, err := ResolveAddress(token.AnyDefinition(), []any{ConversationId(1<<53 - 1)}); err != nil || resolved.Address.Scope.ConversationId != 1<<53-1 {
		t.Fatalf("max safe owner: %#v, %v", resolved, err)
	}
}

// truncatedBy is null when the content was not truncated (truncate.ts TruncationResult).
func TestTruncationResultEncodesAnAbsentLimitAsNull(t *testing.T) {
	result := TruncateHead("ok", TruncationOptions{})
	encoded := jsonOf(t, result)
	if want := `{"content":"ok","truncated":false,"truncatedBy":null,"totalLines":1,"totalBytes":2,"outputLines":1,"outputBytes":2,"lastLinePartial":false,"firstLineExceedsLimit":false,"maxLines":2000,"maxBytes":51200}`; encoded != want {
		t.Fatalf("result = %s\nwant %s", encoded, want)
	}
	var decoded TruncationResult
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != result {
		t.Fatalf("decoded = %#v", decoded)
	}
	truncated := jsonOf(t, TruncateHead("a\nb", TruncationOptions{MaxLines: new(1)}))
	var probe struct {
		TruncatedBy *string `json:"truncatedBy"`
	}
	if err := json.Unmarshal([]byte(truncated), &probe); err != nil || probe.TruncatedBy == nil || *probe.TruncatedBy != "lines" {
		t.Fatalf("truncated = %s", truncated)
	}
}

// packages/durable/src/types.ts:99-100 `DocFamilyToken.definition`: the token exposes the definition it was defined with
// (kind, version, scope, family marker), and an erased token of a family shares it with the typed one.
func TestDocFamilyTokenExposesItsDefinition(t *testing.T) {
	token := DefineDocFamily(DocFamilyDefinition[JsonObject, JsonValue]{
		Family: true,
		Kind:   "notes", Version: 3,
		DocumentSemantics: DocumentSemantics{Scope: ScopeSession},
		Initial:           func(JsonValue) JsonObject { return delta.NewJsonObject(0) },
	})
	definition := token.AnyDefinition()
	if definition.Kind != "notes" || definition.Version != 3 || definition.Scope != ScopeSession || !definition.Family {
		t.Fatalf("definition = %+v", definition)
	}
	var erased AnyDocToken = token
	if erased.AnyDefinition() != definition {
		t.Fatal("the erased family token carries a different definition")
	}
}
