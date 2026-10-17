package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"slices"
	"testing"
)

// A packed member that recovers after its cell died keeps the Extension its runner holds and takes its new handlers and tools, like an isolated restart (adoptConn). Before, recovery replaced only the handlers, so a tool the member no longer registered stayed callable and a new one never appeared.
func TestPackedRecoveryReplacesTheRunnersTools(t *testing.T) {
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	process := &packedProcessState{waitDone: make(chan struct{})}
	process.waitOnce.Do(func() {})
	previous := &managedExt{config: ExtConfig{Name: "member"}, host: h}
	previous.ext = h.buildExtension(previous, &RegisterPayload{Name: "member", Tools: []ToolDecl{{Name: "stale", Parameters: json.RawMessage(`{"type":"object"}`)}}})
	runnerExt := previous.ext
	me := &managedExt{config: ExtConfig{Name: "member"}, host: h, packedProcess: process, supervisor: NewSupervisor(SupervisorConfig{})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		client, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return
		}
		defer func() { _ = client.Close() }()
		_ = writeEnvelopeTo(client, &Envelope{Type: MsgRegister, Register: &RegisterPayload{Name: "member", Tools: []ToolDecl{{Name: "fresh", Parameters: json.RawMessage(`{"type":"object"}`)}}}})
		for {
			if _, err := readEnvelopeFrom(client); err != nil {
				return
			}
		}
	}()
	ctx := context.WithValue(t.Context(), recoveryMembersKey{}, map[string]*managedExt{"member": previous})
	got, err := h.acceptPackedExt(ctx, me, ln)
	if err != nil {
		t.Fatal(err)
	}
	if got != runnerExt {
		t.Fatalf("recovery returned a new Extension; the runner holds %p", runnerExt)
	}
	var names []string
	for _, tool := range got.RegisteredTools() {
		names = append(names, tool.Definition.Name)
	}
	if !slices.Equal(names, []string{"fresh"}) {
		t.Fatalf("the runner's tools after recovery = %v, want [fresh]", names)
	}
}
