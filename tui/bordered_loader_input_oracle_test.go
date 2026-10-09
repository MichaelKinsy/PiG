package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type loaderProbe struct {
	Cancellable bool                `json:"cancellable"`
	Bindings    map[string][]string `json:"bindings,omitempty"`
	Keys        []string            `json:"keys"`
}

type loaderProbeState struct {
	Aborted bool `json:"aborted"`
	Aborts  int  `json:"aborts"`
}

// Pi bordered-loader.ts handleInput: a cancellable loader forwards every key to its CancellableLoader, which aborts
// once on tui.select.cancel; a non-cancellable loader ignores input and its signal never aborts.
func TestBorderedLoaderHandleInputMatchesPi(t *testing.T) {
	probes := []loaderProbe{
		{Cancellable: true, Keys: []string{"x", "\r", "\x1b", "\x1b"}},
		{Cancellable: true, Keys: []string{"\x03"}},
		{Cancellable: false, Keys: []string{"\x1b", "\x03"}},
		{Cancellable: true, Bindings: map[string][]string{KBSelectCancel: {"ctrl+q"}}, Keys: []string{"\x1b", "\x03", "\x11"}},
		{Cancellable: false, Bindings: map[string][]string{KBSelectCancel: {"ctrl+q"}}, Keys: []string{"\x11"}},
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/bordered_loader_input.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		States []loaderProbeState `json:"states"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previous := GetKeybindings()
	t.Cleanup(func() { SetKeybindings(previous) })
	for i, probe := range probes {
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		loader := NewBorderedLoader(nil, ActiveTheme(), "Working", BorderedLoaderOptions{Cancellable: new(probe.Cancellable)})
		aborts := 0
		loader.OnAbort = func() { aborts++ }
		var got []loaderProbeState
		for _, key := range probe.Keys {
			loader.HandleInput(key)
			aborted := loader.CancellableContext() != nil && loader.CancellableContext().Aborted()
			got = append(got, loaderProbeState{Aborted: aborted, Aborts: aborts})
		}
		loader.Dispose()
		if !reflect.DeepEqual(got, expected[i].States) {
			t.Errorf("probe %d %+v:\n  Pig %+v\n  Pi  %+v", i, probe, got, expected[i].States)
		}
	}
}
