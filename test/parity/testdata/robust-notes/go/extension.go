// Package robustnotes is the Go SDK version of the robust-notes fixture (../node/index.mjs).
package robustnotes

import (
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/google/shlex"
)

type picker struct {
	items    []string
	selected int
}

func (p *picker) Render(width int) []string {
	lines := []string{fmt.Sprintf("robust pick (%d)", width)}
	for i, item := range p.items {
		marker := " "
		if i == p.selected {
			marker = ">"
		}
		lines = append(lines, marker+" "+item)
	}
	return lines
}

func (p *picker) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	switch data {
	case "j":
		p.selected = (p.selected + 1) % len(p.items)
	case "s":
		return sdk.RemoteComponentResult{Done: true, Value: p.items[p.selected]}, nil
	case "q":
		return sdk.RemoteComponentResult{Done: true}, nil
	}
	return sdk.RemoteComponentResult{}, nil
}

func Extension() *sdk.Extension {
	ext := sdk.New("go")
	toolRuns := 0
	ext.Flag("robust-prefix", sdk.FlagOptions{Description: "Prefix for robust_words output", Type: sdk.FlagString, Default: "rw"})
	ext.Tool("robust_words", "Split a command line into words.", sdk.Schema{
		"type":       "object",
		"properties": map[string]any{"line": map[string]any{"type": "string"}},
		"required":   []string{"line"},
	}, func(ctx sdk.Context, params map[string]any) (any, error) {
		line, _ := params["line"].(string)
		words, err := shlex.Split(line)
		if err != nil {
			return nil, err
		}
		prefix, err := ctx.GetFlag("robust-prefix")
		if err != nil {
			return nil, err
		}
		return fmt.Sprintf("%v: %s", prefix, strings.Join(words, "|")), nil
	})
	ext.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus("robust", "robust: ready")
		return nil, nil
	})
	ext.OnToolResult(func(ctx sdk.Context, data map[string]any) (any, error) {
		if data["toolName"] != "robust_words" {
			return nil, nil
		}
		toolRuns++
		ctx.SetStatus("robust", fmt.Sprintf("robust: tools=%d", toolRuns))
		return nil, nil
	})
	ext.Command("robust-settings", "Show the robust-notes settings", func(ctx sdk.Context, _ string) error {
		settings, err := ctx.GetSettings()
		if err != nil {
			return err
		}
		greeting := "none"
		if notes, ok := settings["robustNotes"].(map[string]any); ok {
			if value, ok := notes["greeting"].(string); ok {
				greeting = value
			}
		}
		prefix, err := ctx.GetFlag("robust-prefix")
		if err != nil {
			return err
		}
		ctx.Notify(fmt.Sprintf("robust settings: greeting=%s prefix=%v", greeting, prefix), "info")
		return nil
	})
	ext.Command("robust-note", "Save a note", func(ctx sdk.Context, args string) error {
		if err := ctx.AppendEntry("robust-note", map[string]any{"text": args}); err != nil {
			return err
		}
		entries, err := ctx.GetEntries()
		if err != nil {
			return err
		}
		notes := 0
		for _, raw := range entries {
			var entry struct {
				Type       string `json:"type"`
				CustomType string `json:"customType"`
			}
			if err := json.Unmarshal(raw, &entry); err != nil {
				return err
			}
			if entry.Type == "custom" && entry.CustomType == "robust-note" {
				notes++
			}
		}
		ctx.Notify(fmt.Sprintf("robust note %d: %s", notes, args), "info")
		return nil
	})
	ext.Command("robust-pick", "Pick an item in an overlay", func(ctx sdk.Context, _ string) error {
		choice, err := ctx.Custom(&picker{items: []string{"alpha", "beta", "gamma"}}, sdk.RemoteOverlayOptions{Overlay: true})
		if err != nil {
			return err
		}
		if choice == nil {
			choice = "nothing"
		}
		ctx.Notify(fmt.Sprintf("robust picked: %v", choice), "info")
		return nil
	})
	return ext
}
