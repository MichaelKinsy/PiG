package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// quietPrintFaux streams one write call in 16-byte deltas and then a closing text, with a fixed timestamp so two runs
// can be compared. Each chunk waits for a timer, so the state an update carries does not depend on how many reactions
// its consumers take (JSON mode awaits stdout per event; text mode does not).
func quietPrintFaux() *ai.FauxProviderHandle {
	size := 4
	timestamp := int64(1_700_000_000_000)
	faux := ai.NewFauxProvider(ai.FauxConfig{TokensPerSecond: 1_000_000, TokenSize: &ai.FauxTokenSize{Min: &size, Max: &size}, Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 1_000_000, MaxTokens: 100_000}}})
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Timestamp: &timestamp, StopReason: string(ai.StopReasonToolUse), Content: []ai.FauxContentBlock{
			ai.FauxText("writing it"),
			ai.FauxToolCall("write", map[string]any{"path": "out.txt", "content": strings.Repeat("streamed text ", 80)}, &ai.FauxToolCallOptions{ID: "call_quiet"}),
		}}),
		ai.FauxStaticStep(ai.FauxResponse{Timestamp: &timestamp, Content: []ai.FauxContentBlock{ai.FauxText("done")}}),
	})
	return faux
}

// runQuietPrint runs print mode with one extension whose message_update handler records each event it receives, and
// returns those records and stdout.
func runQuietPrint(t *testing.T, mode string) (updates []string, stdout string) {
	t.Helper()
	services := quietPrintServices(t)
	faux := quietPrintFaux()
	configureFauxAuth(t, services, faux.ID())
	host := printModeRuntime{Services: services, Session: coding.SessionStartOptions{Model: faux.GetModel(), ResourceLoader: coding.NoResources, SessionDir: t.TempDir()}}
	host.Extensions = []extension.Extension{{Path: "/quiet", Handlers: map[string][]extension.HandlerFn{"message_update": {func(args ...any) (any, error) {
		encoded, err := json.Marshal(args[0])
		if err != nil {
			t.Error(err)
		}
		// Each faux provider instance has its own api name; nothing else differs between two runs.
		updates = append(updates, strings.ReplaceAll(string(encoded), `"api":"`+string(faux.API())+`"`, `"api":"faux"`))
		return nil, nil
	}}}}}
	var out, stderr bytes.Buffer
	if err := startPrintModeErr(context.Background(), host, printModeOptions{Mode: mode, InitialMessage: "write the file", stdout: &out, stderr: &stderr}); err != nil || stderr.Len() != 0 {
		t.Fatalf("print mode %s: %v, stderr %q", mode, err, stderr.String())
	}
	return updates, out.String()
}

func quietPrintServices(t *testing.T) *coding.AgentSessionServices {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	return services
}

// An extension message_update handler observes the updates of a `-p` text run: it receives every one, as in JSON mode,
// where stdout observes them too, and each as JSON mode delivers it.
func TestPrintTextModeDeliversMessageUpdatesToAnExtensionHandler(t *testing.T) {
	textUpdates, _ := runQuietPrint(t, "text")
	jsonUpdates, stdout := runQuietPrint(t, "json")
	printed := strings.Count(stdout, `"type":"message_update"`)
	if printed < 50 || len(jsonUpdates) != printed {
		t.Fatalf("json mode printed %d message_update lines and its handler saw %d, want the same streamed run", printed, len(jsonUpdates))
	}
	if len(textUpdates) != len(jsonUpdates) {
		t.Fatalf("text mode handler saw %d updates, json mode's %d", len(textUpdates), len(jsonUpdates))
	}
	for i := range textUpdates {
		if textUpdates[i] != jsonUpdates[i] {
			t.Fatalf("update %d differs:\ntext %s\njson %s", i, textUpdates[i], jsonUpdates[i])
		}
	}
}
