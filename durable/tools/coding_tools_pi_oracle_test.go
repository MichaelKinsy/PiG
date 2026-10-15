package tools

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Runs Pi's createCodingTools and createReadTool (packages/durable/src/tools/{index,read}.ts, Node, types stripped) and PiG's
// CreateCodingTools and CreateReadTool over the same cases: the extension's tools, their descriptions and schemas with and
// without an image processor, and what read returns for an image with a stub processor (the notes for a conversion and a
// resize, the cannot-prepare error, the model's limits, a model without image input) or without one.
type piReadCase struct {
	name     string
	path     string
	file     []byte
	prepared *PreparedImage // answered by the stub processor; nil with processor set: it cannot prepare the image
	// processor is whether read gets an image processor.
	processor bool
	model     *ai.Model
}

func piOracle(t *testing.T, cases []map[string]any) []json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	root := testenv.ModuleRoot(t)
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/coding_tools_oracle.mjs", filepath.Join(root, ".upstream", "current", "packages"))
	cmd.Env = append(cmd.Environ(), "PI_INTEROP_DIR="+filepath.Join(root, "durable", "interop")+"/")
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var out []json.RawMessage
	if err := json.Unmarshal(output, &out); err != nil || len(out) != len(cases) {
		t.Fatalf("oracle output %s: %v", output, err)
	}
	return out
}

func normalized(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCodingToolsMatchPi(t *testing.T) {
	results := piOracle(t, []map[string]any{{"op": "tools", "images": false}, {"op": "tools", "images": true}})
	for i, options := range []*ReadToolOptions{nil, {Images: &stubProcessor{}}} {
		var pi struct {
			Name  string `json:"name"`
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Parameters  any    `json:"parameters"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(results[i], &pi); err != nil {
			t.Fatal(err)
		}
		extension := CreateCodingTools(options)
		if extension.Name != pi.Name || len(extension.Tools) != len(pi.Tools) {
			t.Fatalf("images=%v: PiG %q with %d tools, Pi %q with %d", options != nil, extension.Name, len(extension.Tools), pi.Name, len(pi.Tools))
		}
		for j, tool := range extension.Tools {
			want := pi.Tools[j]
			if tool.Name != want.Name || tool.Description != want.Description || !reflect.DeepEqual(normalized(t, tool.Parameters), want.Parameters) {
				t.Errorf("images=%v tool %d: PiG %q %q %v, Pi %q %q %v", options != nil, j, tool.Name, tool.Description, normalized(t, tool.Parameters), want.Name, want.Description, want.Parameters)
			}
		}
	}
}

func TestReadOfImagesMatchesPi(t *testing.T) {
	visionless := &ai.Model{ID: "test", Input: []string{"text"}}
	narrow := &ai.Model{ID: "test", Input: []string{"text", "image"}, InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 4, MaxBytes: 1000}}}}
	png := pngOfSize
	cases := []piReadCase{
		{name: "no processor, png within the limits", path: "a.png", file: must(base64.StdEncoding.DecodeString(onePixelPNG))},
		{name: "no processor, png wider than the limit", path: "wide.png", file: png(2001, 2)},
		{name: "no processor, png taller than the limit", path: "tall.png", file: png(2, 2001)},
		{name: "no processor, png too many bytes", path: "big.png", file: append(png(10, 10), make([]byte, 4*1024*1024)...)},
		{name: "no processor, png whose size cannot be read", path: "cut.png", file: png(8, 6)[:20]},
		{name: "no processor, bmp", path: "a.bmp", file: emptyBMP()},
		{name: "no processor, model without image input", path: "a.png", file: must(base64.StdEncoding.DecodeString(onePixelPNG)), model: visionless},
		{name: "no processor, model limits", path: "a.png", file: png(8, 6), model: narrow},
		{name: "processor converted and resized", path: "wide.png", file: png(4000, 1000), processor: true, prepared: &PreparedImage{Data: "AAAA", MimeType: "image/jpeg", ConvertedFrom: "image/png", Resized: &ImageResize{From: ImageSize{4000, 1000}, To: ImageSize{2000, 500}}}},
		{name: "processor resized, scale rounds", path: "p.png", file: png(1920, 1080), processor: true, prepared: &PreparedImage{Data: "AAAA", MimeType: "image/png", Resized: &ImageResize{From: ImageSize{1920, 1080}, To: ImageSize{1000, 562}}}},
		{name: "processor resized, scale 1.005", path: "p.png", file: png(2000, 1000), processor: true, prepared: &PreparedImage{Data: "AAAA", MimeType: "image/png", Resized: &ImageResize{From: ImageSize{2010, 1000}, To: ImageSize{2000, 996}}}},
		// read.ts:159 scales by the widths only; 2125/1000 = 2.125 is an exact binary tie that toFixed(2) rounds up to 2.13.
		{name: "processor resized, the axes scale differently", path: "p.png", file: png(2000, 1000), processor: true, prepared: &PreparedImage{Data: "AAAA", MimeType: "image/png", Resized: &ImageResize{From: ImageSize{2125, 1000}, To: ImageSize{1000, 400}}}},
		{name: "processor converted only", path: "a.bmp", file: emptyBMP(), processor: true, prepared: &PreparedImage{Data: "QQ==", MimeType: "image/png", ConvertedFrom: "image/bmp"}},
		{name: "processor unchanged", path: "a.png", file: png(8, 6), processor: true, prepared: &PreparedImage{Data: "QQ==", MimeType: "image/png"}},
		{name: "processor cannot prepare", path: "broken.png", file: png(4, 4), processor: true},
		{name: "processor, model without image input", path: "a.png", file: png(4, 4), processor: true, prepared: &PreparedImage{Data: "QQ==", MimeType: "image/png"}, model: visionless},
		{name: "processor sees the model's limits", path: "a.png", file: png(8, 6), processor: true, prepared: &PreparedImage{Data: "QQ==", MimeType: "image/png"}, model: narrow},
	}
	var wire []map[string]any
	for _, c := range cases {
		wc := map[string]any{"op": "read", "path": c.path, "images": "none", "files": map[string]string{c.path: base64.StdEncoding.EncodeToString(c.file)}}
		if c.processor {
			wc["images"] = "stub"
			if c.prepared == nil {
				wc["prepared"] = nil
			} else {
				prepared := map[string]any{"data": c.prepared.Data, "mimeType": c.prepared.MimeType}
				if c.prepared.ConvertedFrom != "" {
					prepared["convertedFrom"] = c.prepared.ConvertedFrom
				}
				if c.prepared.Resized != nil {
					prepared["resized"] = map[string]any{
						"from": map[string]int{"width": c.prepared.Resized.From.Width, "height": c.prepared.Resized.From.Height},
						"to":   map[string]int{"width": c.prepared.Resized.To.Width, "height": c.prepared.Resized.To.Height},
					}
				}
				wc["prepared"] = prepared
			}
		}
		if c.model != nil {
			input := c.model.Input
			model := map[string]any{"input": input}
			if c.model.InputLimits != nil {
				resize := map[string]int{}
				if r := c.model.InputLimits.Images.Resize; r.MaxWidth != 0 {
					resize["maxWidth"] = r.MaxWidth
				}
				if r := c.model.InputLimits.Images.Resize; r.MaxBytes != 0 {
					resize["maxBytes"] = r.MaxBytes
				}
				model["resize"] = resize
			}
			wc["model"] = model
		}
		wire = append(wire, wc)
	}
	results := piOracle(t, wire)
	for i, c := range cases {
		var pi struct {
			Output      []json.RawMessage `json:"output"`
			IsError     bool              `json:"isError"`
			Diagnostics []struct {
				Severity string `json:"severity"`
				Code     string `json:"code"`
				Message  string `json:"message"`
			} `json:"diagnostics"`
			Seen struct {
				MimeType string `json:"mimeType"`
				Size     int    `json:"size"`
				Limits   struct{ MaxWidth, MaxHeight, MaxBytes int }
			} `json:"seen"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(results[i], &pi); err != nil || pi.Error != "" {
			t.Fatalf("%s: Pi: %v %s", c.name, err, pi.Error)
		}
		executionEnv := createEnv(t)
		writeText(t, executionEnv, c.path, c.file)
		stub := &stubProcessor{prepared: c.prepared}
		var options *ReadToolOptions
		if c.processor {
			options = &ReadToolOptions{Images: stub}
		}
		api := &fakeAPI{env: executionEnv, agent: durable.Agent{Model: &durable.ModelRef{Provider: "p", ModelId: "test"}}, models: knownModels{model: modelOrDefault(c.model)}}
		got, err := runWith(CreateReadTool(options), map[string]any{"path": c.path}, api, background)
		if err != nil {
			t.Errorf("%s: PiG error %v", c.name, err)
			continue
		}
		if got.IsError != nil && *got.IsError != pi.IsError || got.IsError == nil && pi.IsError {
			t.Errorf("%s: PiG isError %v, Pi %v", c.name, got.IsError, pi.IsError)
		}
		if gotOut, wantOut := normalized(t, got.Output), normalized(t, rawList(pi.Output)); !reflect.DeepEqual(gotOut, wantOut) && (len(got.Output) != 0 || len(pi.Output) != 0) {
			t.Errorf("%s: PiG output %v, Pi %v", c.name, gotOut, wantOut)
		}
		if len(got.Diagnostics) != len(pi.Diagnostics) {
			t.Errorf("%s: PiG diagnostics %+v, Pi %+v", c.name, got.Diagnostics, pi.Diagnostics)
			continue
		}
		for j, d := range got.Diagnostics {
			if want := pi.Diagnostics[j]; string(d.Severity) != want.Severity || d.Code != want.Code || d.Message != want.Message {
				t.Errorf("%s: diagnostic %d: PiG %+v, Pi %+v", c.name, j, d, want)
			}
		}
		if c.processor && (stub.gotMime != pi.Seen.MimeType || stub.gotSize != pi.Seen.Size ||
			stub.gotLimit != (ImageLimits{MaxWidth: pi.Seen.Limits.MaxWidth, MaxHeight: pi.Seen.Limits.MaxHeight, MaxBytes: pi.Seen.Limits.MaxBytes})) {
			t.Errorf("%s: the processor was given %q %d %+v, Pi's %+v", c.name, stub.gotMime, stub.gotSize, stub.gotLimit, pi.Seen)
		}
	}
}

func rawList(raws []json.RawMessage) []any {
	out := make([]any, 0, len(raws))
	for _, raw := range raws {
		var v any
		_ = json.Unmarshal(raw, &v)
		out = append(out, v)
	}
	return out
}

func modelOrDefault(model *ai.Model) *ai.Model {
	if model != nil {
		return model
	}
	return &ai.Model{ID: "test", Input: []string{"text", "image"}}
}

// emptyBMP is a BMP the format check accepts (a 24-bit DIB header) with no pixels.
func emptyBMP() []byte {
	bmp := make([]byte, 70)
	copy(bmp, "BM")
	bmp[2] = byte(len(bmp))
	bmp[10] = 54
	bmp[14] = 40
	bmp[26] = 1
	bmp[28] = 24
	return bmp
}
