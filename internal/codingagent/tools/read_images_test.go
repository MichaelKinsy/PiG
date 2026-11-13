package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestReadImageModelProfileOverridesFallback(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 80, 40))); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.png"), encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		limits *ai.ModelInputLimits
		resize bool
		width  int
		omit   bool
	}{
		{"fallback", nil, true, 40, false},
		{"model", &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 20}}}, true, 20, false},
		{"disabled", &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 20}}}, false, 80, false},
		{"impossible", &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxBytes: 1}}}, true, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &ReadTool{CWD: dir, AutoResizeImages: &tc.resize, ResizeOptions: &ai.ModelImageResizeOptions{MaxWidth: 40}}
			supports := false
			ctx := agent.WithToolEnvironment(context.Background(), agent.ToolEnvironment{InputLimits: tc.limits, SupportsImages: &supports})
			got, err := tool.Execute(ctx, "read", json.RawMessage(`{"path":"image.png"}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			// Pi 4be4cb4f5e9c packages/coding-agent/test/tools.test.ts:240: the text says nothing of the model's image support.
			if !strings.Contains(got.Text(), "Read image file [image/png]") || strings.Contains(got.Text(), "does not support images") {
				t.Fatalf("content=%q", got.Text())
			}
			if tc.omit {
				// read.ts names the model only beside an image it attached.
				if len(got.Images()) != 0 || !strings.Contains(got.Text(), "omitted") || got.Details != nil {
					t.Fatalf("failed image result=%#v", got)
				}
				return
			}
			// The environment names no model: Pi writes `details = { imageUnsupportedByModel: ctx.model.id }` for an id of "" too,
			// in the session (the tool's own details) and for extensions (the SDK's).
			for name, details := range map[string]any{"session": got.Details, "extension": extension.ToolResultDetailsFor(got.Details)} {
				if raw, err := json.Marshal(details); err != nil || string(raw) != `{"imageUnsupportedByModel":""}` {
					t.Fatalf("%s details = %s, %v", name, raw, err)
				}
			}
			if len(got.Images()) != 1 {
				t.Fatalf("images=%#v", got.Images())
			}
			data, err := base64.StdEncoding.DecodeString(got.Images()[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			decoded, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if decoded.Bounds().Dx() != tc.width || decoded.Bounds().Dy() != tc.width/2 {
				t.Fatalf("size=%v", decoded.Bounds())
			}
		})
	}
}
