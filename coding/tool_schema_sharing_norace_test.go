//go:build !race

package coding

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// schemaScaleProvider answers every request with one text reply.
type schemaScaleProvider struct{}

func (schemaScaleProvider) ID() string { return "fake" }
func (schemaScaleProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	message := sessionTestMessage("fake", "ok", ai.StopReasonStop, "")
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}
func (schemaScaleProvider) Close() error { return nil }

// schemaScaleParameters is a JSON schema of the given number of string properties, each with a description as long as a real tool's.
func schemaScaleParameters(properties int) []byte {
	members := make([]string, 0, properties)
	for i := range properties {
		members = append(members, fmt.Sprintf(`"field%d":{"type":"string","description":"argument %d of the extension tool, with a description of realistic length for a schema"}`, i, i))
	}
	return []byte(`{"type":"object","properties":{` + strings.Join(members, ",") + `},"required":["field0"]}`)
}

// promptCost is the bytes and allocations one prompt costs a session with the four default built-in tools and three extension tools
// whose schemas have the given number of properties, averaged over prompts after the tools are declared.
func promptCost(t *testing.T, properties int) (bytes, mallocs float64) {
	t.Helper()
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ext := extension.Extension{Tools: map[string]extension.RegisteredTool{}}
	for _, name := range []string{"ext_one", "ext_two", "ext_three"} {
		definition := registryTool(name, name, "extension tool "+name, "Run "+name)
		definition.Parameters = schemaScaleParameters(properties)
		ext.Tools[name] = extension.RegisteredTool{Definition: definition}
		ext.ToolOrder = append(ext.ToolOrder, name)
	}
	ext.InitializeToolRegistry()
	session, err := NewSession(services, SessionOptions{Model: fakeModelWithProvider(schemaScaleProvider{}), NoSession: true, Runner: inproc.NewRunner([]extension.Extension{ext}, services.CWD())})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	if got := len(session.ActiveToolNames()); got != 7 {
		t.Fatalf("active tools = %d, want 7", got)
	}
	for i := range 3 {
		if _, err := session.Send(t.Context(), fmt.Sprintf("warm %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	const prompts = 8
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range prompts {
		if _, err := session.Send(t.Context(), fmt.Sprintf("hello %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	return float64(after.TotalAlloc-before.TotalAlloc) / prompts, float64(after.Mallocs-before.Mallocs) / prompts
}

// TestPromptHoldsToolSchemasOnce: a prompt reads the declared tools without copying their parameter trees. The fixture is a
// session with the four default built-in tools and three extension tools whose schemas have 12 properties each, no session file
// and a one-reply provider. Measured over 8 prompts after the tools are declared (the declaration is Pi's one copy, transcript.ts
// toToolDeclaration, and is not part of a prompt):
//
//	before the cut (9cd955810e): 489,600 to 494,900 B and 3,230 to 3,244 allocations per prompt
//	with the schemas held once:  370,000 to 372,400 B and 2,421 to 2,422 allocations per prompt
//
// The three copies the cut removed (the compaction estimate's system message, the compaction preparation's projection and each
// built-in tool's Schema() in the loadout comparison) are the 121 KB and 815 allocations between them. The limits sit between the
// two states, 14 % above the cut's figures and 14 % below the old ones: the measurements repeat to within 1.5 % in bytes and 0.5 %
// in allocations, and the margin leaves room for unrelated work on the prompt path without letting one schema copy back in.
// (What is left is mostly the stringified length of the declared tools that the compaction estimate counts, which is not a copy.)
func TestPromptHoldsToolSchemasOnce(t *testing.T) {
	const maxBytes, maxMallocs = 425_000, 2_800
	bytes, mallocs := promptCost(t, 12)
	t.Logf("per prompt: %.0f B, %.0f allocs", bytes, mallocs)
	if bytes > maxBytes {
		t.Errorf("a prompt allocates %.0f B, limit %d: the tool schemas are copied per prompt again", bytes, maxBytes)
	}
	if mallocs > maxMallocs {
		t.Errorf("a prompt makes %.0f allocations, limit %d: the tool schemas are copied per prompt again", mallocs, maxMallocs)
	}
}
