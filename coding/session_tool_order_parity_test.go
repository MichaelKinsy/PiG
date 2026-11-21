package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestFreshAndCloneToolOrderMatchesPi verifies that a fresh Session, its Clone,
// and the session after Reload all send tools in Pi's order:
// [built-ins, extension tools, caller's tools]
// (upstream agent-session.ts:3660-3666, 3702, 3733-3736:
// allCustomTools = [...registeredTools, ...this._customTools], then appended
// after initialActiveToolNames).
func TestFreshAndCloneToolOrderMatchesPi(t *testing.T) {
	t.Parallel()

	extTool := extension.RegisteredTool{
		Definition: extension.ToolDefinition{
			Name:        "ext_probe",
			Label:       "Ext Probe",
			Description: "extension tool",
			Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}
	callerTool := extension.ToolDefinition{
		Name:        "caller_probe",
		Label:       "Caller Probe",
		Description: "caller custom tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}

	// 1. Unit check at createSessionToolRegistry level.
	t.Run("createSessionToolRegistry", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			t.Fatalf("CreateAgentSessionServices: %v", err)
		}
		defer services.Close()
		runner := inproc.NewRunner([]extension.Extension{
			{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}},
		}, dir)
		_, activeTools, err := createSessionToolRegistry(services, SessionOptions{
			Runner:      runner,
			CustomTools: []extension.ToolDefinition{callerTool},
		})
		if err != nil {
			t.Fatalf("createSessionToolRegistry: %v", err)
		}
		got := make([]string, len(activeTools))
		for i, tool := range activeTools {
			got[i] = tool.Name()
		}
		want := []string{"read", "bash", "edit", "write", "ext_probe", "caller_probe"}
		if !slices.Equal(got, want) {
			t.Fatalf("createSessionToolRegistry active tools = %v, want %v", got, want)
		}
	})

	// 2. Provider request boundary: fresh, clone, and after reload.
	t.Run("provider request tools order", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			t.Fatalf("CreateAgentSessionServices: %v", err)
		}
		configureFauxAuth(t, services)
		defer services.Close()

		var recorded [][]string
		provider := &scriptedProvider{
			wrap: true,
			responses: []scriptedResponse{
				func(messages []ai.Message) *ai.AssistantMessage {
					schemas := ai.GetCurrentTools(ai.TranscriptMessages[ai.Message](messages))
					names := make([]string, len(schemas))
					for i, s := range schemas {
						names[i] = s.Name
					}
					recorded = append(recorded, names)
					return &ai.AssistantMessage{
						Provider:   "faux",
						Model:      "faux-1",
						Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}},
						StopReason: ai.StopReasonStop,
					}
				},
			},
		}
		captureToolNames := func(t *testing.T, session *Session, prompt string) []string {
			t.Helper()
			before := len(recorded)
			if err := session.Prompt(context.Background(), prompt); err != nil {
				t.Fatalf("Prompt(%q): %v", prompt, err)
			}
			if len(recorded) <= before {
				t.Fatalf("expected provider request for %q", prompt)
			}
			return recorded[len(recorded)-1]
		}
		runner := inproc.NewRunner([]extension.Extension{
			{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}},
		}, dir)
		session, err := NewSession(services, SessionOptions{
			Model:       &ai.Model{ID: "faux-1", Provider: provider},
			Runner:      runner,
			CustomTools: []extension.ToolDefinition{callerTool},
			NoSession:   true,
		})
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		defer func() { _ = session.Close() }()

		want := []string{"read", "bash", "edit", "write", "ext_probe", "caller_probe"}
		// Clone before prompting so the clone starts from the registry rather than replaying a prior turn's transcript.
		cloned, err := session.Clone()
		if err != nil {
			t.Fatalf("Clone: %v", err)
		}
		defer func() { _ = cloned.Close() }()

		freshGot := captureToolNames(t, session, "fresh")
		if !slices.Equal(freshGot, want) {
			t.Errorf("fresh request tools = %v, want %v", freshGot, want)
		}

		cloneGot := captureToolNames(t, cloned, "clone")
		if !slices.Equal(cloneGot, want) {
			t.Errorf("clone request tools = %v, want %v", cloneGot, want)
		}
		if !slices.Equal(freshGot, cloneGot) {
			t.Errorf("fresh != clone: fresh=%v clone=%v", freshGot, cloneGot)
		}

		// Refresh the runner (the reload path) and verify the session keeps Pi's order.
		session.ReplaceRunner(inproc.NewRunner([]extension.Extension{
			{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}},
		}, dir))
		reloadGot := captureToolNames(t, session, "reload")
		if !slices.Equal(reloadGot, want) {
			t.Errorf("reload request tools = %v, want %v", reloadGot, want)
		}
		// Clone after a prompt under the cure: the clone replays the source's recorded
		// transcript tools, keeping Pi's order.
		clonedAfterPrompt, err := session.Clone()
		if err != nil {
			t.Fatalf("Clone after prompt: %v", err)
		}
		defer func() { _ = clonedAfterPrompt.Close() }()
		cloneAfterPromptGot := captureToolNames(t, clonedAfterPrompt, "clone-after-prompt")
		if !slices.Equal(cloneAfterPromptGot, want) {
			t.Errorf("clone-after-prompt request tools = %v, want %v", cloneAfterPromptGot, want)
		}

		// Clone before any prompt after reordering the active tool list on the source:
		// Session.Clone must seed the clone with s.ActiveToolNames() so the clone does
		// not fall back to the registry's default order.
		customOrder := []string{"read", "bash", "edit", "write", "caller_probe", "ext_probe"}
		customSession, err := NewSession(services, SessionOptions{
			Model:                  &ai.Model{ID: "faux-1", Provider: provider},
			Runner:                 inproc.NewRunner([]extension.Extension{{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}}}, dir),
			CustomTools:            []extension.ToolDefinition{callerTool},
			InitialActiveToolNames: customOrder,
			NoSession:              true,
		})
		if err != nil {
			t.Fatalf("NewSession (custom active order): %v", err)
		}
		defer func() { _ = customSession.Close() }()
		customClone, err := customSession.Clone()
		if err != nil {
			t.Fatalf("Clone (custom active order): %v", err)
		}
		defer func() { _ = customClone.Close() }()
		customCloneGot := captureToolNames(t, customClone, "clone-custom-active-order")
		if !slices.Equal(customCloneGot, customOrder) {
			t.Errorf("clone-before-prompt with custom active order = %v, want %v", customCloneGot, customOrder)
		}
	})

	// 3. Resumed session whose persisted transcript recorded the pre-cure order
	// [read, bash, edit, write, caller_probe, ext_probe], and a clone made after
	// prompting that resumed session.
	// Rule: resuming an existing session keeps its recorded tool order on provider
	// requests, writes no ToolsAdded/ToolsRemoved churn record, keeps the request's
	// serialized tool prefix byte-equal, and cloning it after a prompt replays that
	// recorded order.
	t.Run("resumed old-order transcript preserves order and prefix", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
		if err != nil {
			t.Fatalf("CreateAgentSessionServices: %v", err)
		}
		configureFauxAuth(t, services)
		defer services.Close()

		oldOrder := []string{"read", "bash", "edit", "write", "caller_probe", "ext_probe"}

		newCaptureProvider := func() (*scriptedProvider, *[][]string, *[][]byte) {
			var namesLog [][]string
			var prefixLog [][]byte
			p := &scriptedProvider{
				wrap: true,
				responses: []scriptedResponse{
					func(messages []ai.Message) *ai.AssistantMessage {
						schemas := ai.GetCurrentTools(ai.TranscriptMessages[ai.Message](messages))
						names := make([]string, len(schemas))
						for i, s := range schemas {
							names[i] = s.Name
						}
						raw, err := json.Marshal(schemas)
						if err != nil {
							t.Errorf("marshal tool schemas: %v", err)
						}
						namesLog = append(namesLog, names)
						prefixLog = append(prefixLog, raw)
						return &ai.AssistantMessage{
							Provider:   "faux",
							Model:      "faux-1",
							Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "ok"}},
							StopReason: ai.StopReasonStop,
						}
					},
				},
			}
			return p, &namesLog, &prefixLog
		}

		// Produce the persisted transcript with the pre-cure order using the existing
		// InitialActiveToolNames seam, which writes the exact same toolsAdded JSONL
		// record that 0.7.0 / pre-cure code wrote.
		preProvider, preNames, prePrefixes := newCaptureProvider()
		preRunner := inproc.NewRunner([]extension.Extension{
			{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}},
		}, dir)
		preSession, err := NewSession(services, SessionOptions{
			Model:                  &ai.Model{ID: "faux-1", Provider: preProvider},
			Runner:                 preRunner,
			CustomTools:            []extension.ToolDefinition{callerTool},
			InitialActiveToolNames: oldOrder,
			SessionDir:             dir,
		})
		if err != nil {
			t.Fatalf("NewSession (pre-cure fixture): %v", err)
		}
		if err := preSession.Prompt(context.Background(), "pre-upgrade"); err != nil {
			t.Fatalf("pre-upgrade Prompt: %v", err)
		}
		sessionPath := preSession.Path()
		if err := preSession.Close(); err != nil {
			t.Fatalf("pre-upgrade Close: %v", err)
		}
		if len(*preNames) != 1 || !slices.Equal((*preNames)[0], oldOrder) {
			t.Fatalf("pre-upgrade request tools = %v, want %v", *preNames, oldOrder)
		}

		countToolDeltaRecords := func(data []byte) (added, removed int) {
			for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				var entry struct {
					Type    string `json:"type"`
					Message struct {
						Role         string             `json:"role"`
						ToolsAdded   []ai.ToolSchema    `json:"toolsAdded"`
						ToolsRemoved []ai.ToolReference `json:"toolsRemoved"`
					} `json:"message"`
				}
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					continue
				}
				if entry.Type == "message" && entry.Message.Role == "system" {
					if len(entry.Message.ToolsAdded) > 0 {
						added++
					}
					if len(entry.Message.ToolsRemoved) > 0 {
						removed++
					}
				}
			}
			return added, removed
		}

		beforeBytes, err := os.ReadFile(sessionPath)
		if err != nil {
			t.Fatalf("ReadFile before resume: %v", err)
		}
		addedBefore, removedBefore := countToolDeltaRecords(beforeBytes)
		if addedBefore != 1 || removedBefore != 0 {
			t.Fatalf("before resume tool delta records: added=%d removed=%d, want 1/0", addedBefore, removedBefore)
		}

		// Resume under the cured code with the same caller and extension tools (no InitialActiveToolNames).
		resumedProvider, resumedNames, resumedPrefixes := newCaptureProvider()
		resumedRunner := inproc.NewRunner([]extension.Extension{
			{Name: "probe-ext", ToolOrder: []string{"ext_probe"}, Tools: map[string]extension.RegisteredTool{"ext_probe": extTool}},
		}, dir)
		resumed, err := NewSession(services, SessionOptions{
			Model:       &ai.Model{ID: "faux-1", Provider: resumedProvider},
			Runner:      resumedRunner,
			CustomTools: []extension.ToolDefinition{callerTool},
			ResumePath:  sessionPath,
			SessionDir:  dir,
		})
		if err != nil {
			t.Fatalf("NewSession (resumed): %v", err)
		}
		defer func() { _ = resumed.Close() }()

		if err := resumed.Prompt(context.Background(), "post-upgrade"); err != nil {
			t.Fatalf("resumed Prompt: %v", err)
		}
		if len(*resumedNames) != 1 {
			t.Fatalf("expected 1 resumed provider request, got %d", len(*resumedNames))
		}
		// (i) Provider request keeps the transcript's recorded old order.
		if !slices.Equal((*resumedNames)[0], oldOrder) {
			t.Errorf("resumed request tools = %v, want old order %v", (*resumedNames)[0], oldOrder)
		}
		// (ii) Transcript gained no ToolsAdded / ToolsRemoved churn record on resume.
		afterBytes, err := os.ReadFile(sessionPath)
		if err != nil {
			t.Fatalf("ReadFile after resume: %v", err)
		}
		addedAfter, removedAfter := countToolDeltaRecords(afterBytes)
		if addedAfter != addedBefore || removedAfter != removedBefore {
			t.Errorf("resumed transcript tool delta records changed: added %d->%d, removed %d->%d", addedBefore, addedAfter, removedBefore, removedAfter)
		}
		// (iii) Serialized tool prefix is byte-equal before and after the cure.
		if !bytes.Equal((*prePrefixes)[0], (*resumedPrefixes)[0]) {
			t.Errorf("resumed tool prefix bytes differ from pre-cure request (len %d vs %d)", len((*resumedPrefixes)[0]), len((*prePrefixes)[0]))
		}

		// Clone made after prompting the resumed old-order session replays the recorded old order.
		clonedOld, err := resumed.Clone()
		if err != nil {
			t.Fatalf("Clone of resumed old-order session: %v", err)
		}
		defer func() { _ = clonedOld.Close() }()
		if err := clonedOld.Prompt(context.Background(), "clone-of-old-order"); err != nil {
			t.Fatalf("clonedOld Prompt: %v", err)
		}
		if len(*resumedNames) != 2 {
			t.Fatalf("expected 2 provider requests after cloning resumed session, got %d", len(*resumedNames))
		}
		if !slices.Equal((*resumedNames)[1], oldOrder) {
			t.Errorf("clone-after-prompt of old-order session tools = %v, want %v", (*resumedNames)[1], oldOrder)
		}
		if !bytes.Equal((*prePrefixes)[0], (*resumedPrefixes)[1]) {
			t.Errorf("clone-after-prompt of old-order session tool prefix bytes differ from pre-cure request")
		}
	})
}
