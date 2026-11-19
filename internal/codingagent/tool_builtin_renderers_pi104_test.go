// The golden rows are Pi 1.0.4 rendering on a POSIX host: paths and file URLs differ on Windows.

//go:build !windows

package codingagent

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

type piRenderStep struct {
	Op     string          `json:"op"`
	Tool   string          `json:"tool"`
	Of     string          `json:"of"`
	Args   json.RawMessage `json:"args"`
	Ctx    piRenderContext `json:"ctx"`
	Result *struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
		} `json:"content"`
		Details map[string]any `json:"details"`
		IsError bool           `json:"isError"`
	} `json:"result"`
}

type piRenderContext struct {
	Expanded     bool  `json:"expanded"`
	IsPartial    bool  `json:"isPartial"`
	IsError      bool  `json:"isError"`
	ArgsComplete *bool `json:"argsComplete"`
	ShowImages   *bool `json:"showImages"`
}

type piRenderCase struct {
	Name  string         `json:"name"`
	Steps []piRenderStep `json:"steps"`
	Lines [][]string     `json:"lines"`
}

func piRenderResult(step piRenderStep) agent.AgentToolResult {
	result := agent.AgentToolResult{IsError: step.Result.IsError}
	for _, block := range step.Result.Content {
		if block.Type == "image" {
			result.Content = append(result.Content, ai.ImageContent{Data: block.Data, MimeType: block.MimeType})
			continue
		}
		result.Content = append(result.Content, ai.TextContent{Text: block.Text})
	}
	if step.Result.Details != nil {
		result.Details = step.Result.Details
	}
	return result
}

// shortTempDir keeps the probe's path widths comparable: the golden rows wrap at a fixed width, so a long TMPDIR would
// wrap a row whose path Pi's probe kept on one line. The directory is created under /tmp, not os.TempDir.
func shortTempDir(t *testing.T) string {
	t.Helper()
	//portlint:allow hardtmp a short fixed root keeps the golden rows at Pi's width; os.TempDir can be long (macOS, CI)
	dir, err := os.MkdirTemp("/tmp", "g")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// previewExpected reports whether an edit call step starts the preview computation.
func previewExpected(step piRenderStep) bool {
	_, _, ok := editPreviewInput(step.Args)
	complete := step.Ctx.ArgsComplete == nil || *step.Ctx.ArgsComplete
	return ok && complete
}
