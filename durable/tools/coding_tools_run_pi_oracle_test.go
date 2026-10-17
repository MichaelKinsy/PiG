package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// Runs the write, edit, bash and read tools of Pi's createCodingTools (packages/durable/src/tools/index.ts, Node, types stripped)
// and of PiG's CreateCodingTools over the same files and arguments, and compares what each answers (output, structured output,
// details, isError, diagnostics, the text the tool streamed, or the error it throws) and the files each leaves behind.
type piRunCase struct {
	name  string
	tool  string
	args  map[string]any
	files map[string]string
}

func TestCodingToolsRunLikePi(t *testing.T) {
	notes := "alpha\nbeta\ngamma\nbeta\n"
	cases := []piRunCase{
		{name: "write a new file", tool: "write", args: map[string]any{"path": "new.txt", "content": "hello\n"}},
		{name: "write creates parent directories", tool: "write", args: map[string]any{"path": "a/b/c.txt", "content": "deep"}},
		{name: "write replaces a file", tool: "write", args: map[string]any{"path": "notes.txt", "content": "x"}, files: map[string]string{"notes.txt": notes}},
		{name: "write an empty file", tool: "write", args: map[string]any{"path": "empty.txt", "content": ""}},
		{name: "edit one occurrence", tool: "edit", args: map[string]any{"path": "notes.txt", "edits": []any{map[string]any{"oldText": "alpha", "newText": "ALPHA"}}}, files: map[string]string{"notes.txt": notes}},
		{name: "edit several regions", tool: "edit", args: map[string]any{"path": "notes.txt", "edits": []any{map[string]any{"oldText": "alpha", "newText": "A"}, map[string]any{"oldText": "gamma", "newText": "G\nG2"}}}, files: map[string]string{"notes.txt": notes}},
		{name: "edit text that is not there", tool: "edit", args: map[string]any{"path": "notes.txt", "edits": []any{map[string]any{"oldText": "delta", "newText": "D"}}}, files: map[string]string{"notes.txt": notes}},
		{name: "edit text that occurs twice", tool: "edit", args: map[string]any{"path": "notes.txt", "edits": []any{map[string]any{"oldText": "beta", "newText": "B"}}}, files: map[string]string{"notes.txt": notes}},
		{name: "edit overlapping regions", tool: "edit", args: map[string]any{"path": "notes.txt", "edits": []any{map[string]any{"oldText": "alpha\nbeta", "newText": "1"}, map[string]any{"oldText": "beta\ngamma", "newText": "2"}}}, files: map[string]string{"notes.txt": notes}},
		{name: "edit a file that is not there", tool: "edit", args: map[string]any{"path": "missing.txt", "edits": []any{map[string]any{"oldText": "a", "newText": "b"}}}},
		{name: "edit that changes nothing", tool: "edit", args: map[string]any{"path": "notes.txt", "edits": []any{map[string]any{"oldText": "alpha", "newText": "alpha"}}}, files: map[string]string{"notes.txt": notes}},
		{name: "bash prints", tool: "bash", args: map[string]any{"command": "echo hi; echo there"}},
		{name: "bash combines stdout and stderr (a pause keeps the two pipes in order)", tool: "bash", args: map[string]any{"command": "echo out; sleep 0.1; echo err 1>&2"}},
		{name: "bash exits with a code", tool: "bash", args: map[string]any{"command": "echo before; exit 3"}},
		{name: "bash runs in the working directory", tool: "bash", args: map[string]any{"command": "cat notes.txt"}, files: map[string]string{"notes.txt": notes}},
		{name: "bash with no output", tool: "bash", args: map[string]any{"command": "true"}},
		{name: "read a text file", tool: "read", args: map[string]any{"path": "notes.txt"}, files: map[string]string{"notes.txt": notes}},
		{name: "read with offset and limit", tool: "read", args: map[string]any{"path": "notes.txt", "offset": 2.0, "limit": 2.0}, files: map[string]string{"notes.txt": notes}},
		{name: "read past the end", tool: "read", args: map[string]any{"path": "notes.txt", "offset": 9.0}, files: map[string]string{"notes.txt": notes}},
		{name: "read a file that is not there", tool: "read", args: map[string]any{"path": "missing.txt"}},
	}
	wire := make([]map[string]any, len(cases))
	for i, c := range cases {
		files := map[string]string{}
		for name, text := range c.files {
			files[name] = base64.StdEncoding.EncodeToString([]byte(text))
		}
		wire[i] = map[string]any{"op": "run", "tool": c.tool, "args": c.args, "files": files}
	}
	results := piOracle(t, wire)
	extension := CreateCodingTools(nil)
	for i, c := range cases {
		var pi struct {
			Output           any               `json:"output"`
			StructuredOutput any               `json:"structuredOutput"`
			Details          any               `json:"details"`
			IsError          bool              `json:"isError"`
			Diagnostics      []map[string]any  `json:"diagnostics"`
			Streamed         string            `json:"streamed"`
			Files            map[string]string `json:"files"`
			Error            string            `json:"error"`
		}
		if err := json.Unmarshal(results[i], &pi); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		executionEnv := createEnv(t)
		for name, text := range c.files {
			path := filepath.Join(executionEnv.Cwd(), name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var tool *durable.ToolRegistration
		for _, candidate := range extension.Tools {
			if candidate.Name == c.tool {
				tool = candidate
			}
		}
		if tool == nil {
			t.Fatalf("%s: CreateCodingTools has no %s tool", c.name, c.tool)
		}
		api := &fakeAPI{env: executionEnv}
		got, err := runWith(tool, c.args, api, context.Background())
		files := map[string]string{}
		_ = filepath.WalkDir(executionEnv.Cwd(), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr == nil && !entry.IsDir() {
				data, readErr := os.ReadFile(path)
				if readErr == nil {
					relative, _ := filepath.Rel(executionEnv.Cwd(), path)
					files[filepath.ToSlash(relative)] = base64.StdEncoding.EncodeToString(data)
				}
			}
			return nil
		})
		if !reflect.DeepEqual(files, orEmpty(pi.Files)) {
			t.Errorf("%s: PiG left files %v, Pi %v", c.name, files, pi.Files)
		}
		if pi.Error != "" || err != nil {
			if err == nil || pi.Error == "" || scrubDir(err.Error(), executionEnv.Cwd()) != pi.Error {
				t.Errorf("%s: PiG error %v, Pi %q", c.name, err, pi.Error)
			}
			continue
		}
		if gotError := got.IsError != nil && *got.IsError; gotError != pi.IsError {
			t.Errorf("%s: PiG isError %v, Pi %v", c.name, gotError, pi.IsError)
		}
		if gotOut, want := scrubbed(t, normalized(t, got.Output), executionEnv.Cwd()), pi.Output; !reflect.DeepEqual(gotOut, want) && (len(got.Output) != 0 || !isEmptyList(want)) {
			t.Errorf("%s: PiG output %v, Pi %v", c.name, gotOut, want)
		}
		var structured any
		if got.HasStructuredOutput {
			structured = scrubbed(t, normalized(t, got.StructuredOutput), executionEnv.Cwd())
		}
		if !reflect.DeepEqual(structured, pi.StructuredOutput) {
			t.Errorf("%s: PiG structuredOutput %v, Pi %v", c.name, structured, pi.StructuredOutput)
		}
		var details any
		if got.HasDetails {
			details = scrubbed(t, normalized(t, got.Details), executionEnv.Cwd())
		}
		if !reflect.DeepEqual(details, pi.Details) {
			t.Errorf("%s: PiG details %v, Pi %v", c.name, details, pi.Details)
		}
		var diagnostics []map[string]any
		for _, d := range append(append([]any{}, diagnosticsOf(api)...), diagnosticsOfResult(got)...) {
			diagnostics = append(diagnostics, d.(map[string]any))
		}
		if !reflect.DeepEqual(orEmptyDiagnostics(diagnostics), orEmptyDiagnostics(pi.Diagnostics)) {
			t.Errorf("%s: PiG diagnostics %v, Pi %v", c.name, diagnostics, pi.Diagnostics)
		}
		if streamed := scrubDir(strings.Join(api.output, ""), executionEnv.Cwd()); streamed != pi.Streamed {
			t.Errorf("%s: PiG streamed %q, Pi %q", c.name, streamed, pi.Streamed)
		}
	}
}

func orEmpty(files map[string]string) map[string]string {
	if files == nil {
		return map[string]string{}
	}
	return files
}

func isEmptyList(value any) bool {
	list, ok := value.([]any)
	return value == nil || ok && len(list) == 0
}

func orEmptyDiagnostics(list []map[string]any) []map[string]any {
	if list == nil {
		return []map[string]any{}
	}
	return list
}

func diagnosticsOf(api *fakeAPI) []any {
	out := make([]any, 0, len(api.diagnostics))
	for _, d := range api.diagnostics {
		out = append(out, diagnosticMap(d.Severity, d.Code, d.Message))
	}
	return out
}

func diagnosticsOfResult(result runResult) []any {
	out := make([]any, 0, len(result.Diagnostics))
	for _, d := range result.Diagnostics {
		out = append(out, diagnosticMap(d.Severity, d.Code, d.Message))
	}
	return out
}

func diagnosticMap(severity any, code, message string) map[string]any {
	m := map[string]any{"severity": reflectString(severity), "message": message}
	if code != "" {
		m["code"] = code
	}
	return m
}

func reflectString(value any) string { return reflect.ValueOf(value).String() }

// scrubDir names the temporary directory of a run the same on both sides.
func scrubDir(text, directory string) string { return strings.ReplaceAll(text, directory, "<dir>") }

func scrubbed(t *testing.T, value any, directory string) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal([]byte(scrubDir(string(encoded), directory)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
