package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// executeCodemode without a session lays the script's output out for the model: header, `==> text N/M <==` markers, the console block, joined adjacent text,
// images after text, the truncation note and the script error. The pinned Pi's executeCodemode and Execute run the same scripts; the content blocks must be equal
// once the wall time and the temporary file names are masked.
func TestExecuteOutputMatchesPi(t *testing.T) {
	png := `iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==`
	scripts := []string{
		`return 1`, `return "s"`, `return undefined`, `text("a")`, `text("a"); text("b")`, `text("a"); return "b"`, `text("a"); text({ b: 1 }); return [1]`,
		`console.log("c1"); console.log("c2")`, `text("t"); console.log("c"); text("u")`, `console.log("c"); return 5`, `console.error("e"); throw new Error("x")`,
		`throw new Error("boom")`, `throw "plain"`, `text("before"); throw new Error("after")`, `null.x`, `return (`,
		`text("a\n"); text("b")`, `text(""); text("x")`, `text("   "); text("y")`, `text("a"); text(""); text("")`,
		`image("data:image/png;base64,` + png + `"); text("t")`, `text("t"); image("data:image/png;base64,` + png + `"); text("u")`,
		`image({ type: "image", data: "` + png + `", mimeType: "image/png" }); image({ type: "image", data: "` + png + `", mimeType: "image/png" })`,
		`text("x".repeat(50000))`, `text("x".repeat(200000))`, `text("line\n".repeat(30000))`, `text("é😀".repeat(30000))`, `for (let i = 0; i < 3000; i++) console.log("log line " + i)`,
		`text("a".repeat(30000)); text("b".repeat(30000)); text("c".repeat(30000))`, `console.log("x".repeat(100000)); return "y".repeat(100000)`,
		`return "x".repeat(41000)`, `return "x".repeat(40000)`, `return "x".repeat(39999)`, `text("😀".repeat(20001))`, `text("\ud800".repeat(40001))`,
		`image("data:image/png;base64,` + png + `"); text("x".repeat(200000))`, `text("x".repeat(200000)); throw new Error("late")`,
		`// codemode: maxOutputTokens=10
text("word ".repeat(100))`, `// codemode: maxOutputTokens=0
text("x")`, `// codemode: maxOutputTokens=1
text("x".repeat(100))`, `// codemode: maxOutputTokens=-5
text("x")`, `// codemode: maxOutputTokens=1.5
text("x".repeat(100))`, `// codemode: maxOutputTokens=abc
text("x")`, `// codemode: unknown=1
text("x")`, `// codemode: maxOutputTokens=10
// codemode: maxOutputTokens=3
text("x".repeat(100))`,
		`store("k", 1); return load("k")`, `exit(2)`, `text("a"); exit("done"); text("b")`,
	}
	cache := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cache, "quickjs-wasi"), 0o755); err != nil {
		t.Fatal(err)
	}
	wasm, err := filepath.Abs("../../../../codemode/assets/quickjs.wasm")
	if err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, wasm, filepath.Join(cache, "quickjs-wasi", "quickjs.wasm"))
	scriptPath, err := filepath.Abs("testdata/execute_oracle.mjs")
	if err != nil {
		t.Fatal(err)
	}
	type probe struct {
		Code string `json:"code"`
	}
	probes := make([]probe, len(scripts))
	for i, s := range scripts {
		probes[i] = probe{s}
	}
	type block struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		MimeType string `json:"mimeType,omitempty"`
		Data     string `json:"data,omitempty"`
	}
	type outcome struct {
		Content        []block `json:"content"`
		FullOutputPath string  `json:"fullOutputPath"`
	}
	var want []outcome
	pioracle.Run(t, `
const { spawnSync } = await import("node:child_process");
const child = spawnSync(process.execPath, [process.env.ORACLE_SCRIPT], { input: JSON.stringify(input), encoding: "utf8", maxBuffer: 1 << 28, env: { ...process.env, NODE_PATH: process.env.ORACLE_NODE_PATH } });
if (child.status !== 0) throw new Error(child.stderr);
emit(JSON.parse(child.stdout));
`, probes, &want, "ORACLE_SCRIPT="+scriptPath, "ORACLE_NODE_PATH="+cache)
	wall := regexp.MustCompile(`Wall time \d+\.\d seconds`)
	paths := regexp.MustCompile(`(/[^\s"')]*pi-[^\s"')]*|/[^\s"')]*/tmp[^\s"')]*|/tmp/[^\s"')]*)`)
	// testdata/execute_oracle.mjs names each unpaired surrogate of Pi's text as <U+d83d>; Go's WTF-8 form is named the same.
	decoded := func(s string) string {
		var out []rune
		units := jsstring.ToUTF16(s)
		for i := 0; i < len(units); i++ {
			switch u := units[i]; {
			case u >= 0xd800 && u < 0xdc00 && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff:
				out = append(out, utf16.DecodeRune(rune(u), rune(units[i+1])))
				i++
			case utf16.IsSurrogate(rune(u)):
				out = append(out, []rune(fmt.Sprintf("<U+%x>", u))...)
			default:
				out = append(out, rune(u))
			}
		}
		return string(out)
	}
	mask := func(s string) string {
		return paths.ReplaceAllString(wall.ReplaceAllString(decoded(s), "Wall time N seconds"), "<path>")
	}
	bad := 0
	for i, script := range scripts {
		// A VM keeps its slot until it has exited, after Execute has returned (the pinned Pi's driver above waits for its workers' exits for the same reason).
		codemodeWorkers.waitIdle()
		params, _ := json.Marshal(map[string]string{"code": script})
		result, err := Execute(context.Background(), "call", params, nil, Options{})
		if err != nil {
			t.Fatal(err)
		}
		var got outcome
		for _, item := range result.Content {
			switch c := item.(type) {
			case ai.TextContent:
				got.Content = append(got.Content, block{Type: "text", Text: mask(c.Text)})
			case ai.ImageContent:
				got.Content = append(got.Content, block{Type: "image", MimeType: c.MimeType, Data: c.Data})
			}
		}
		for j := range want[i].Content {
			want[i].Content[j].Text = mask(want[i].Content[j].Text)
		}
		g, _ := json.Marshal(got.Content)
		w, _ := json.Marshal(want[i].Content)
		if !bytes.Equal(g, w) {
			if bad++; bad <= 8 {
				at := 0
				for at < len(g) && at < len(w) && g[at] == w[at] {
					at++
				}
				t.Errorf("%q differ at byte %d (PiG %d bytes, Pi %d)\n  PiG ...%.120q\n  Pi  ...%.120q", script, at, len(g), len(w), g[max(0, at-30):], w[max(0, at-30):])
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d of %d scripts differ", bad, len(scripts))
	}
}

// execute.ts truncateText is `text.slice(0, maxChars - 3) + "..."`: a cut inside a surrogate pair leaves the high half.
func TestTruncateTextCutsAtCodeUnits(t *testing.T) {
	text := "aaaaa😀bbbb"
	for _, tc := range []struct {
		max  int
		want string
	}{
		{20, text},
		{11, text},
		{10, "aaaaa😀..."},
		{9, "aaaaa" + jsstring.FromUTF16([]uint16{0xd83d}) + "..."},
		{8, "aaaaa..."},
		{3, "..."},
	} {
		if got := truncateText(text, tc.max); got != tc.want {
			t.Errorf("truncateText(%q, %d) = %q, want %q", text, tc.max, got, tc.want)
		}
	}
}
