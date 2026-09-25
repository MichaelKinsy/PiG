package correspondence

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestExtractTypeScriptCurrentPin(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := ExtractTypeScript(
		context.Background(),
		nodePath,
		filepath.Join(root, "parity/interface-extractor/src/extract-correspondence.mjs"),
		filepath.Join(root, ".upstream/current"),
		coding.UpstreamVersion,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Tables) != 1 {
		t.Fatalf("table count = %d, want 1", len(inventory.Tables))
	}
	var ids []string
	for _, item := range inventory.Tables[0].Items {
		ids = append(ids, item.ID)
	}
	wantIDs := []string{
		"autocompact", "show-images", "image-width-cells", "auto-resize-images", "block-images", "skill-commands",
		"show-hardware-cursor", "editor-padding", "output-padding", "autocomplete-max-visible", "clear-on-shrink",
		"terminal-progress", "steering-mode", "follow-up-mode", "transport", "http-idle-timeout", "cache-warming-mode",
		"hide-thinking", "mermaid-rendering", "cache-miss-notices", "collapse-changelog", "quiet-startup",
		"install-telemetry", "default-project-trust", "double-escape-action", "tree-filter-mode", "warnings",
		"model-thinking", "tui-mode", "fullscreen-exit-output", "fullscreen-scrollbar", "fullscreen-copy-on-select", "theme",
	}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("settings IDs = %v, want %v", ids, wantIDs)
	}
	productionCallbacks := inventory.Tables[0].ProductionCallbacks
	if len(productionCallbacks) != len(wantIDs) {
		t.Fatalf("production callback count = %d, want %d", len(productionCallbacks), len(wantIDs))
	}
	var autocompact ProductionCallback
	for _, callback := range productionCallbacks {
		if callback.ID == "autocompact" {
			autocompact = callback
			break
		}
	}
	if autocompact.Handler != "onAutoCompactChange" || len(autocompact.Segments) != 1 || !slices.Equal([]string{autocompact.Segments[0].Calls[0].Callee, autocompact.Segments[0].Calls[1].Callee}, []string{"this.session.setAutoCompactionEnabled", "this.footer.setAutoCompactEnabled"}) {
		t.Fatalf("autocompact production callback = %#v", autocompact)
	}
	var promptNames []string
	for _, constant := range inventory.Constants {
		promptNames = append(promptNames, constant.Name)
	}
	wantPromptNames := []string{"SUMMARIZATION_PROMPT", "TURN_PREFIX_SUMMARIZATION_PROMPT", "UPDATE_SUMMARIZATION_PROMPT", "SUMMARIZATION_SYSTEM_PROMPT"}
	if !reflect.DeepEqual(promptNames, wantPromptNames) {
		t.Fatalf("prompt names = %v, want %v", promptNames, wantPromptNames)
	}
	if len(inventory.Functions) != 10 {
		t.Fatalf("function count = %d, want 10", len(inventory.Functions))
	}
	compact := inventory.Functions[0]
	if compact.Name != "compact" || !compact.Async || !reflect.DeepEqual(compact.CancellationInputs, []string{"signal"}) || len(compact.Callers) != 1 || compact.Callers[0].Symbol != "_runDefaultCompaction" {
		t.Fatalf("compact identity = %#v", compact)
	}
	var ordered []string
	for _, call := range compact.Calls {
		if call.Callee == "generateSummaryWithUsage" || call.Callee == "generateTurnPrefixSummary" || call.Callee == "combineUsage" {
			ordered = append(ordered, call.Callee)
		}
	}
	if !reflect.DeepEqual(ordered, []string{"generateSummaryWithUsage", "generateTurnPrefixSummary", "combineUsage", "generateSummaryWithUsage"}) {
		t.Fatalf("compact ordered calls = %v", ordered)
	}
	var stateProfile []string
	for _, transition := range compact.Transitions {
		if transition.Target == "historyText" || transition.Target == "summary" || transition.Kind == "error" || transition.Kind == "return" {
			stateProfile = append(stateProfile, transition.Kind+":"+transition.Target)
		}
	}
	if !reflect.DeepEqual(stateProfile, []string{"bind:historyText", "update:historyText", "update:summary", "update:summary", "update:summary", "error:", "return:"}) {
		t.Fatalf("compact state profile = %v", stateProfile)
	}
	for _, function := range inventory.Functions {
		if function.Name == "applyOverrides" {
			if len(function.Callers) != 0 {
				t.Fatalf("applyOverrides callers = %#v, want none", function.Callers)
			}
			continue
		}
		if len(function.Callers) == 0 {
			t.Fatalf("function %s has no compiled production caller", function.Name)
		}
	}
	wantManager := []string{"applyOverrides", "mergeSettings", "persistScopedSettings", "reload", "saveGlobal", "saveProject", "setProjectTrusted"}
	var manager []string
	orchestration := false
	for _, function := range inventory.Functions {
		if function.Kind == "settings-manager" {
			manager = append(manager, function.Name)
		}
		if function.Kind == "settings-orchestration" && function.Name == "settingsOrchestration" {
			orchestration = true
		}
	}
	slices.Sort(manager)
	if !reflect.DeepEqual(manager, wantManager) || !orchestration {
		t.Fatalf("settings manager functions/orchestration = %v/%t, want %v/true", manager, orchestration, wantManager)
	}
}

func TestDecodeInventoryRejectsUnknownFields(t *testing.T) {
	const hash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	input := `{
		"source":{"language":"typescript","revision":"0.84.0"},
		"tables":[{"id":"table:x","path":"x.ts","owner":"X","sourceHash":"` + hash + `","orderProfile":"all","items":[{"id":"x","label":"X","description":"X","descriptionExpression":"","currentValueExpression":"x","values":["x"],"valuesExpression":"","submenuExpression":"","gate":"","positionExpression":"","path":"x.ts","startLine":1,"endLine":1,"sourceHash":"` + hash + `"}],"callbacks":[]}],
		"constants":[],
		"unknown":true
	}`
	_, err := DecodeInventory(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeInventory() error = %v, want unknown field", err)
	}
}

func TestDecodeInventoryRequiresAllCurrentCollections(t *testing.T) {
	const hash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	input := `{
		"source":{"language":"typescript","revision":"0.84.0"},
		"tables":[],
		"constants":[{"id":"constant:x","path":"x.ts","name":"PROMPT","value":"x","utf16Length":1,"sourceHash":"` + hash + `","valueHash":"` + hash + `","startLine":1,"endLine":1}]
	}`
	_, err := DecodeInventory(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "collections must be arrays") {
		t.Fatalf("DecodeInventory() error = %v, want collection requirement", err)
	}
}

func TestBoundedBufferCapsExtractorOutput(t *testing.T) {
	buffer := newBoundedBuffer(4)
	written, err := buffer.Write([]byte("abcdef"))
	if err != nil || written != 6 {
		t.Fatalf("Write() = %d, %v, want 6, nil", written, err)
	}
	if !buffer.overflow || buffer.String() != "abcd" {
		t.Fatalf("buffer = %q overflow=%t, want abcd and true", buffer.String(), buffer.overflow)
	}
}
