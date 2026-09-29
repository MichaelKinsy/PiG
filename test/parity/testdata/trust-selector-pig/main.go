package main

import (
	"encoding/json"
	"os"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

func main() {
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	codingagent.DefaultKeybindingsManager()
	type snapshot struct {
		Name      string                      `json:"name"`
		Lines     []string                    `json:"lines"`
		Selection *codingagent.TrustSelection `json:"selection"`
	}
	var out []snapshot
	for _, tc := range []struct {
		name, cwd, savedPath string
		saved, trusted       bool
		key                  string
	}{{"saved", "/project", "/project", true, true, "\x1b[B"}, {"new", "/project", "", false, false, "\n"}, {"ancestor", "/parent/project/nested", "/parent", true, true, ""}, {"parent", "/parent/project", "/parent", true, true, "\n"}} {
		var saved *codingagent.ProjectTrustStoreEntry
		if tc.savedPath != "" {
			saved = &codingagent.ProjectTrustStoreEntry{Path: tc.savedPath, Decision: tc.saved}
		}
		var selection *codingagent.TrustSelection
		component := codingagent.NewTrustSelectorComponent(codingagent.TrustSelectorOptions{Cwd: tc.cwd, SavedDecision: saved, ProjectTrusted: tc.trusted, OnSelect: func(value codingagent.TrustSelection) { selection = &value }})
		out = append(out, snapshot{Name: tc.name + "-before", Lines: component.Render(120)})
		component.HandleInput(tc.key)
		out = append(out, snapshot{Name: tc.name + "-after", Lines: component.Render(120), Selection: selection})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(out); err != nil {
		panic(err)
	}
}
