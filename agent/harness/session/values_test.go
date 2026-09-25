package session_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestBoundAddressesValidateComponents(t *testing.T) {
	scalar, err := session.NewValue[string]("app.state", "")
	if err != nil || scalar.StoredAddressBase != (session.StoredAddressBase{Namespace: "app.state", Key: "", Kind: session.KindValue}) {
		t.Fatalf("value = %#v, %v", scalar, err)
	}
	events, err := session.NewList[int]("app.events", "workspace")
	if err != nil || events.StoredAddressBase != (session.StoredAddressBase{Namespace: "app.events", Key: "workspace", Kind: session.KindList}) {
		t.Fatalf("list = %#v, %v", events, err)
	}
	if _, err := session.NewValue[any]("", ""); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty namespace err = %v", err)
	}
	if _, err := session.NewValue[any]("app\x00state", ""); err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Fatalf("NUL namespace err = %v", err)
	}
	if _, err := session.NewList[any]("app.events", "bad\x00key"); err == nil || !strings.Contains(err.Error(), "must not contain") {
		t.Fatalf("NUL key err = %v", err)
	}
	reserved, err := session.NewValue[any]("pi.application", "")
	if err != nil || reserved.Namespace != "pi.application" {
		t.Fatalf("constructors perform no ownership check: %#v, %v", reserved, err)
	}
}

func TestBoundAddressHelpersFixWriteTypes(t *testing.T) {
	type counter struct{ Count int }
	scalar := session.MustValue[counter]("app.state", "")
	write := session.SetValue(scalar, counter{Count: 1})
	if _, ok := write.Value.(counter); !ok || write.Namespace != "app.state" {
		t.Fatalf("set write = %#v", write)
	}
	events := session.MustList[string]("app.events", "")
	appended := session.AppendList(events, "created")
	if appended != (session.ListAppendWrite{Namespace: "app.events", Key: "", Value: "created"}) {
		t.Fatalf("append write = %#v", appended)
	}
	if session.DeleteValue(scalar) != (session.ValueDeleteWrite{Namespace: "app.state"}) || session.DeleteList(events) != (session.ListDeleteWrite{Namespace: "app.events"}) {
		t.Fatal("delete writes lost their address")
	}
}

func TestListElementTypesFlowThroughStorageAndSessionReader(t *testing.T) {
	storage := session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 1 }})
	defer func() { _ = storage.Close(background) }()
	type named struct{ Name string }
	events := session.MustList[named]("app.events", "")
	var reader session.SessionReader = storage
	if _, err := storage.Commit(background, []session.Write{session.AppendList(events, named{Name: "created"})}); err != nil {
		t.Fatal(err)
	}
	elements, err := session.ReadList(background, reader, events, nil)
	if err != nil || len(elements) != 1 || elements[0].Value.Name != "created" {
		t.Fatalf("elements = %#v, %v", elements, err)
	}
}

func TestSeparatelyConstructedEqualAddressesNameOneLocation(t *testing.T) {
	storage := session.NewMemoryStorage(&session.MemoryStorageOptions{Now: func() int64 { return 1 }})
	defer func() { _ = storage.Close(background) }()
	type ready struct{ Ready bool }
	first := session.MustValue[ready]("app.state", "workspace")
	second := session.MustValue[ready]("app.state", "workspace")
	if _, err := storage.Commit(background, []session.Write{session.SetValue(first, ready{Ready: true})}); err != nil {
		t.Fatal(err)
	}
	stored, err := session.GetValue(background, storage, second)
	if err != nil || stored == nil || stored.Address != first || !stored.Value.Ready || stored.Seq != 1 {
		t.Fatalf("stored = %#v, %v", stored, err)
	}
}

func TestBuiltInAddressesUseExactReservedNamespacesKeysAndKinds(t *testing.T) {
	_ = func() session.Value[session.LaneConfiguration] { return session.LaneConfig("review") }()
	_ = func() session.Value[*string] { return session.BranchTip("review") }()
	_ = func() session.Value[session.LaneState] { return session.LaneStateValue("review") }()
	_ = func() session.Value[session.OperationResultRecord] { return session.OperationResult("operation") }()
	_ = func() session.Value[session.OperationMeta] { return session.OperationMetaValue("operation") }()
	_ = func() session.Value[session.OperationState] { return session.OperationStateValue("operation") }()
	_ = func() session.Value[map[string]session.JsonValue] {
		return session.OperationToolArgs("operation", "step", 2)
	}()
	_ = func() session.Value[session.JsonValue] {
		return session.OperationToolMemo("operation", "invocation", "name")
	}()
	_ = func() session.Value[session.DurableStructuralPreparation] {
		return session.OperationPreparation("operation", "task")
	}()
	_ = func() session.Value[session.PendingEntry] { return session.PendingEntryValue("entry") }()
	_ = func() session.Value[harness.AgentToolResult] {
		return session.PendingToolOutput("operation", "invocation")
	}()
	_ = func() session.ValueList[ai.AssistantMessageFrame] {
		return session.PendingAssistantFrames("operation", "response")
	}()
	got := []session.StoredAddressBase{
		session.BranchTip("review").Address(),
		session.LaneConfig("review").Address(),
		session.LaneStateValue("review").Address(),
		session.OperationResult("operation").Address(),
		session.OperationMetaValue("operation").Address(),
		session.OperationStateValue("operation").Address(),
		session.OperationToolArgs("operation", "step", 2).Address(),
		session.OperationToolMemo("operation", "invocation", "name").Address(),
		session.OperationPreparation("operation", "task").Address(),
		session.PendingEntryValue("entry").Address(),
		session.PendingToolOutput("operation", "invocation").Address(),
		session.PendingAssistantFrames("operation", "response").Address(),
		session.SessionName.Address(),
		session.EntryLabel("entry").Address(),
	}
	want := []session.StoredAddressBase{
		{Kind: "value", Namespace: "pi.branch.tip", Key: "review"},
		{Kind: "value", Namespace: "pi.lane.config", Key: "review"},
		{Kind: "value", Namespace: "pi.lane.state", Key: "review"},
		{Kind: "value", Namespace: "pi.result", Key: "operation"},
		{Kind: "value", Namespace: "pi.op.meta", Key: "operation"},
		{Kind: "value", Namespace: "pi.op.state", Key: "operation"},
		{Kind: "value", Namespace: "pi.op.tool_args", Key: "operation:step:2"},
		{Kind: "value", Namespace: "pi.op.tool_memo", Key: "operation:invocation:name"},
		{Kind: "value", Namespace: "pi.op.preparation", Key: "operation:task"},
		{Kind: "value", Namespace: "pi.pending.entry", Key: "entry"},
		{Kind: "value", Namespace: "pi.pending.tool_output", Key: "operation:invocation"},
		{Kind: "list", Namespace: "pi.pending.assistant_frame", Key: "operation:response"},
		{Kind: "value", Namespace: "pi.session.name", Key: ""},
		{Kind: "value", Namespace: "pi.entry.label", Key: "entry"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addresses = %#v", got)
	}
}

func TestExactlyTheFiveDocumentedScanPrefixes(t *testing.T) {
	got := []session.StoredAddressBase{
		session.BranchTipInventoryPrefix().Address(),
		session.OperationToolArgsPrefix("operation", nil).Address(),
		session.OperationToolMemoPrefix("operation", new("invocation")).Address(),
		session.OperationPreparationPrefix("operation").Address(),
		session.PendingToolOutputPrefix("operation").Address(),
	}
	want := []session.StoredAddressBase{
		{Kind: "value", Namespace: "pi.branch.tip", Key: ""},
		{Kind: "value", Namespace: "pi.op.tool_args", Key: "operation:"},
		{Kind: "value", Namespace: "pi.op.tool_memo", Key: "operation:invocation:"},
		{Kind: "value", Namespace: "pi.op.preparation", Key: "operation:"},
		{Kind: "value", Namespace: "pi.pending.tool_output", Key: "operation:"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("prefixes = %#v", got)
	}
	if key := session.OperationToolArgsPrefix("operation", new("step")).Key; key != "operation:step:" {
		t.Fatalf("step prefix = %q", key)
	}
	if key := session.OperationToolMemoPrefix("operation", nil).Key; key != "operation:" {
		t.Fatalf("memo prefix = %q", key)
	}
}

func TestListReadOptionsDefaultAndClamp(t *testing.T) {
	resolved, err := session.ResolveListReadOptions(nil)
	if err != nil || resolved.Order != session.OrderAsc || resolved.Limit != 1_000 || resolved.Cursor != nil {
		t.Fatalf("defaults = %#v, %v", resolved, err)
	}
	resolved, err = session.ResolveListReadOptions(&session.ListReadOptions{Limit: new(50_000), Order: session.OrderDesc})
	if err != nil || resolved.Limit != 10_000 || resolved.Order != session.OrderDesc {
		t.Fatalf("clamped = %#v, %v", resolved, err)
	}
	for _, limit := range []int{0, -1, 1 << 60} {
		if _, err := session.ResolveListReadOptions(&session.ListReadOptions{Limit: new(limit)}); err == nil || err.Error() != "List read limit must be a positive safe integer" {
			t.Fatalf("limit %d err = %v", limit, err)
		}
	}
}

func TestStoredFramesDecodeFromRawJSON(t *testing.T) {
	storage := session.NewMemoryStorage(nil)
	defer func() { _ = storage.Close(background) }()
	frames := session.PendingAssistantFrames("operation", "response")
	raw := []byte(`{"type":"text_delta","contentIndex":0,"delta":"hi"}`)
	if _, err := storage.Commit(background, []session.Write{session.ListAppendWrite{Namespace: frames.Namespace, Key: frames.Key, Value: rawMessage(raw)}}); err != nil {
		t.Fatal(err)
	}
	elements, err := session.ReadList(background, storage, frames, nil)
	if err != nil || len(elements) != 1 {
		t.Fatalf("elements = %#v, %v", elements, err)
	}
	if delta, ok := elements[0].Value.(ai.TextDeltaFrame); !ok || delta.Delta != "hi" {
		t.Fatalf("frame = %#v", elements[0].Value)
	}
}

func TestPrefixPreservesExplicitEmptyComponent(t *testing.T) {
	// Upstream tests optional components with === undefined, not truthiness.
	if got := session.OperationToolArgsPrefix("operation", new("")).Key; got != "operation::" {
		t.Errorf("args prefix = %q, want operation::", got)
	}
	if got := session.OperationToolMemoPrefix("operation", new("")).Key; got != "operation::" {
		t.Errorf("memo prefix = %q, want operation::", got)
	}
}
