package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/tools/read.ts, write.ts, ls.ts

// The read, write and ls tools against pinned Pi over seeded directory trees (text with LF, CRLF and no trailing newline, long lines, many lines,
// empty files, binary content, directories, hidden and mixed-case names) and arguments (offsets and limits around every boundary, missing and
// escaping paths, nested writes): the result content and details, the error, and the tree left behind.
func TestFileToolsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(167))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	type file struct {
		Path    string `json:"path"`
		Content string `json:"content,omitempty"`
		Dir     bool   `json:"dir,omitempty"`
	}
	type probe struct {
		Files []file         `json:"files"`
		Tool  string         `json:"tool"`
		Args  map[string]any `json:"args"`
	}
	text := func() string {
		var b strings.Builder
		for range rng.Intn(30) {
			b.WriteString(pick("alpha", "beta gamma", "", "日本語", "🙂 emoji", "tab\there", strings.Repeat("long ", 300), "x"))
			b.WriteString(pick("\n", "\n", "\r\n"))
		}
		if rng.Intn(3) == 0 {
			b.WriteString("tail")
		}
		return b.String()
	}
	names := []string{"a.txt", "B.txt", ".hidden", "dir/inner.txt", "dir/sub/deep.go", "z", "Makefile", "notes.md", "é.txt", "dir2/"}
	var probes []probe
	for range 900 {
		var files []file
		for _, index := range rng.Perm(len(names))[:rng.Intn(8)] {
			name := names[index]
			switch {
			case strings.HasSuffix(name, "/"):
				files = append(files, file{Path: strings.TrimSuffix(name, "/"), Dir: true})
			case rng.Intn(10) == 0:
				files = append(files, file{Path: name, Content: base64.StdEncoding.EncodeToString([]byte{0, 1, 2, 0xff, 0xfe, '\n', 'a'})})
			case rng.Intn(10) == 0: //nolint:gocritic // each case draws a fresh random number, so the two conditions are not the same test
				files = append(files, file{Path: name})
			default:
				files = append(files, file{Path: name, Content: base64.StdEncoding.EncodeToString([]byte(text()))})
			}
		}
		target := pick(append([]string{"a.txt", "dir", ".", "dir/inner.txt", "missing", "../escape.txt", "dir/new/x.txt", ""}, names...)...)
		if len(files) > 0 && rng.Intn(10) < 7 {
			target = files[rng.Intn(len(files))].Path
		}
		var p probe
		p.Files = files
		switch rng.Intn(5) {
		case 3:
			p.Tool, p.Args = "grep", map[string]any{"pattern": pick("alpha", "beta.*", "long", "tab", "x", "日本", "(", "ALPHA", "^$", "emoji")}
			if rng.Intn(2) == 0 {
				p.Args["path"] = target
			}
			if rng.Intn(3) == 0 {
				p.Args["glob"] = pick("*.txt", "**/*.go", "*.md", "dir/*", "B.*")
			}
			if rng.Intn(3) == 0 {
				p.Args["ignoreCase"] = rng.Intn(2) == 0
			}
			if rng.Intn(4) == 0 {
				p.Args["literal"] = true
			}
			// rg searches files in parallel, so the order of its files varies between runs: a limit or context lines are only compared on one file.
			if len(files) > 0 && rng.Intn(2) == 0 {
				p.Args["path"] = files[rng.Intn(len(files))].Path
				if rng.Intn(2) == 0 {
					p.Args["context"] = float64(rng.Intn(4))
				}
				if rng.Intn(2) == 0 {
					p.Args["limit"] = float64(rng.Intn(8))
				}
			}
		case 4:
			p.Tool, p.Args = "find", map[string]any{"pattern": pick("*.txt", "**/*.go", "*", "dir/*", "*.md", "inner*", "**/deep.go", "nomatch", "a.txt")}
			if rng.Intn(2) == 0 {
				p.Args["path"] = target
			}
		case 0:
			p.Tool, p.Args = "read", map[string]any{"path": target}
			if rng.Intn(2) == 0 {
				p.Args["offset"] = float64(rng.Intn(40) - 2)
			}
			if rng.Intn(2) == 0 {
				p.Args["limit"] = float64(rng.Intn(12))
			}
		case 1:
			p.Tool, p.Args = "write", map[string]any{"path": target, "content": text()}
		default:
			p.Tool, p.Args = "ls", map[string]any{"path": target}
			if rng.Intn(3) == 0 {
				p.Args["limit"] = float64(rng.Intn(6))
			}
			if rng.Intn(5) == 0 {
				delete(p.Args, "path")
			}
		}
		probes = append(probes, p)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/file_tools.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Content []map[string]any  `json:"content"`
		Details any               `json:"details"`
		Thrown  string            `json:"thrown"`
		Dir     string            `json:"dir"`
		Tree    map[string]string `json:"tree"`
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures, succeeded := 0, map[string]int{}
	for i, p := range probes {
		parent := t.TempDir()
		dir := filepath.Join(parent, "cwd")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range p.Files {
			full := filepath.Join(dir, f.Path)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if f.Dir {
				if err := os.MkdirAll(full, 0o755); err != nil {
					t.Fatal(err)
				}
				continue
			}
			content, _ := base64.StdEncoding.DecodeString(f.Content)
			if err := os.WriteFile(full, content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		tool, err := CreateTool(p.Tool, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		args, _ := json.Marshal(p.Args)
		result, execErr := tool.Execute(context.Background(), "", args, nil)
		var got struct {
			Content []map[string]any
			Details any
			Thrown  string
		}
		switch {
		case execErr != nil:
			got.Thrown = execErr.Error()
		case result.IsError:
			got.Thrown = result.Text()
		default:
			for _, block := range result.Content {
				raw, _ := json.Marshal(block)
				var decoded map[string]any
				_ = json.Unmarshal(raw, &decoded)
				got.Content = append(got.Content, decoded)
			}
			raw, _ := json.Marshal(result.Details)
			_ = json.Unmarshal(raw, &got.Details)
		}
		tree := map[string]string{}
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || path == dir {
				return nil
			}
			rel, _ := filepath.Rel(dir, path)
			if entry.IsDir() {
				tree[filepath.ToSlash(rel)+"/"] = ""
				return nil
			}
			content, _ := os.ReadFile(path)
			tree[filepath.ToSlash(rel)] = base64.StdEncoding.EncodeToString(content)
			return nil
		})
		if want[i].Thrown == "" {
			succeeded[p.Tool]++
		}
		// The directory a probe runs in appears in messages; Pi ran it in another one.
		if p.Tool == "grep" || p.Tool == "find" {
			sortLines(got.Content)
			sortLines(want[i].Content)
		}
		gotJSON, _ := json.Marshal([]any{got.Content, got.Details, strings.ReplaceAll(strings.ReplaceAll(got.Thrown, dir, "<cwd>"), parent, "<parent>"), tree})
		wantJSON, _ := json.Marshal([]any{want[i].Content, want[i].Details, strings.ReplaceAll(strings.ReplaceAll(want[i].Thrown, want[i].Dir, "<cwd>"), filepath.Dir(want[i].Dir), "<parent>"), want[i].Tree})
		if !bytes.Equal(gotJSON, wantJSON) {
			if failures++; failures <= 5 {
				t.Errorf("probe %d: %s %s (%d files)\n%s", i, p.Tool, args, len(p.Files), firstDiffOf(gotJSON, wantJSON))
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	for _, name := range []string{"read", "write", "ls", "grep", "find"} {
		if succeeded[name] < 40 {
			t.Fatalf("Pi's %s tool succeeded in only %d probes", name, succeeded[name])
		}
	}
}

// firstDiffOf shows each side from the first byte where the two encodings differ.
func firstDiffOf(got, want []byte) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	from := max(i-60, 0)
	cut := func(b []byte) string { return string(b[min(from, len(b)):min(i+240, len(b))]) }
	return " PiG ..." + cut(got) + "\n Pi  ..." + cut(want)
}

// sortLines puts the lines of each text block in order: rg and fd walk files in parallel, so the order of their results varies between runs.
func sortLines(content []map[string]any) {
	for _, block := range content {
		if text, ok := block["text"].(string); ok {
			lines := strings.Split(text, "\n")
			slices.Sort(lines)
			block["text"] = strings.Join(lines, "\n")
		}
	}
}
