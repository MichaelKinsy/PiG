package codingagent

import (
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// pi: packages/coding-agent/src/core/tools/renderers/read.ts renderCall, renderResult; renderers/write.ts renderCall, renderResult

// readWriteCardProbes are the read and write cards of tool-execution.ts with the built-in definitions, one probe per branch of the four renderers and of the
// helpers they call: the path argument (file_path before path, every non-string type), the line range, the compact label of a skill, a context file and a documentation page,
// the collapsed preview and its remaining-lines hint at the ten-line boundary, the language of the path, tabs, carriage returns and trailing blank lines, every truncation
// warning, text and image content, and the write result's error text. Each is drawn collapsed and expanded in both themes at widths 80 and 28.
func readWriteCardProbes(t *testing.T) ([]toolCardProbe, []string) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	if GetReadmePath() == "" {
		t.Fatal("the documentation bundle is not on disk")
	}
	docsRoot := filepath.Dir(GetReadmePath())
	rows := func(n int, prefix string) string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%s %d", prefix, i+1)
		}
		return strings.Join(out, "\n")
	}
	var probes []toolCardProbe
	var names []string
	add := func(name, tool string, args map[string]any, result *toolCardResult, themes []string, widths []int, expanded []bool, tweak func(*toolCardProbe)) {
		for _, theme := range themes {
			for _, width := range widths {
				for _, expand := range expanded {
					probe := toolCardProbe{Tool: tool, Args: args, Result: result, Expanded: expand, Width: width, Theme: theme, CWD: cwd}
					if tweak != nil {
						tweak(&probe)
					}
					probes = append(probes, probe)
					names = append(names, fmt.Sprintf("%s theme=%s width=%d expanded=%v", name, theme, width, expand))
				}
			}
		}
	}
	both, wide := []string{"dark", "light"}, []int{80, 28}
	var (
		bothWidths = wide
		bothStates = []bool{false, true}
		dark       = []string{"dark"}
		w80        = []int{80}
	)
	ok := func(text string) *toolCardResult { return &toolCardResult{Content: cardText(text)} }
	failed := func(text string) *toolCardResult { return &toolCardResult{Content: cardText(text), IsError: true} }

	// read renderCall: the path argument. file_path wins unless it is null; an empty string is not null; any non-string is the invalid-arg text.
	for name, args := range ordered(map[string]map[string]any{
		"path":                      {"path": "notes.txt"},
		"file_path":                 {"file_path": "notes.txt"},
		"file_path wins":            {"file_path": "a.txt", "path": "b.txt"},
		"null file_path falls back": {"file_path": nil, "path": "b.txt"},
		"empty file_path is kept":   {"file_path": "", "path": "b.txt"},
		"number path":               {"path": 5},
		"bool path":                 {"path": true},
		"object path":               {"path": map[string]any{"a": 1}},
		"array path":                {"path": []any{"a"}},
		"number file_path":          {"file_path": 5, "path": "b.txt"},
		"null path":                 {"path": nil},
		"no path":                   {},
		"empty path":                {"path": ""},
		"home path":                 {"path": "~/notes.txt"},
		"absolute path":             {"path": "/etc/hosts"},
		"long path":                 {"path": strings.Repeat("dir/", 30) + "file.txt"},
		"wide path":                 {"path": "日本語/テキスト.txt"},
	}) {
		add("read call "+name, "read", args, nil, both, bothWidths, bothStates, nil)
	}

	// read renderCall: the line range of formatReadLineRange, in the warning colour after the path.
	type rangeCase struct{ offset, limit any }
	for i, c := range []rangeCase{
		{25, 13}, {"25", "13"}, {nil, 13}, {nil, "13"}, {25, nil}, {"25", nil}, {nil, nil}, {"abc", 13}, {25, ""}, {25, "Infinity"}, {25, "abc"}, {"", 13}, {" ", 13},
		{1, 0}, {0, 1}, {0, 0}, {1, 1}, {10, 1}, {-1, 5}, {-5, 5}, {1.5, 2}, {0.1, 0.2}, {" 25 ", " 13 "}, {"0x10", "2"}, {"1e3", "1e2"}, {"+5", "-2"}, {1e21, 1}, {1e21, 1e21}, {true, 5}, {5, true},
		{[]any{1}, 5}, {map[string]any{}, 5}, {"٣", 2}, {"1_0", 2}, {"Infinity", 5}, {"-Infinity", 5}, {"NaN", 5}, {25, "13.5"}, {"010", "010"}, {"\u00a07", 2}, {"7\ufeff", 2}, {"\ufeff", 5}, {"\u2003", 5}, {5, "\ufeff"}, {2147483648, 2}, {9007199254740993.0, 3},
	} {
		args := map[string]any{"path": "notes.txt"}
		if c.offset != nil {
			args["offset"] = c.offset
		}
		if c.limit != nil {
			args["limit"] = c.limit
		}
		add(fmt.Sprintf("read range %d %v,%v", i, c.offset, c.limit), "read", args, nil, dark, w80, []bool{false}, nil)
	}
	// An argument that is JSON null is absent for formatReadLineRange (strict schemas send null for an omitted optional argument).
	add("read range null offset and limit", "read", map[string]any{"path": "notes.txt", "offset": nil, "limit": nil}, nil, dark, w80, []bool{false}, nil)
	add("read range null offset", "read", map[string]any{"path": "notes.txt", "offset": nil, "limit": 4}, nil, dark, w80, []bool{false}, nil)
	add("read range on a wrapped path", "read", map[string]any{"path": strings.Repeat("d/", 20) + "f.txt", "offset": 100, "limit": 50}, nil, both, bothWidths, bothStates, nil)

	// read renderCall: the compact label while collapsed (a skill by its directory, a context file by its cwd-relative path, a documentation page), the plain header when expanded.
	compactPaths := []string{
		"SKILL.md", "skills/demo/SKILL.md", "/abs/skills/x/SKILL.md", "/SKILL.md", "a b/SKILL.md", "日本/SKILL.md", "skill.md", "SKILL.md.bak", "x/SKILL.MD",
		"AGENTS.md", "sub/AGENTS.md", "../AGENTS.md", "/AGENTS.md", "AGENTS.override.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD", "claude.md", "Agents.md", "AGENTS.md.bak", "docs/CLAUDE.md",
		"~/AGENTS.md", "@AGENTS.md", "a/./b/../AGENTS.md", "deep/er/path/to/a/very/long/directory/name/that/keeps/going/AGENTS.md", "dir/", "",
	}
	for _, path := range compactPaths {
		for _, rng := range []map[string]any{{}, {"offset": 3, "limit": 4}, {"offset": "9"}} {
			args := map[string]any{"path": path}
			for k, v := range rng {
				args[k] = v
			}
			add(fmt.Sprintf("read compact %q %v", path, rng), "read", args, nil, both, bothWidths, bothStates, nil)
		}
	}
	// An empty or missing path has no classification, whatever its working directory is called.
	for _, dir := range []string{"/work/SKILL.md", "/work/AGENTS.md", "/work/skills/demo/SKILL.md"} {
		for _, args := range []map[string]any{{"path": ""}, {}, {"path": nil}} {
			add(fmt.Sprintf("read compact in %s with %v", dir, args), "read", args, nil, dark, w80, []bool{false}, func(p *toolCardProbe) { p.CWD = dir })
		}
		add("read compact in "+dir+" with a relative file", "read", map[string]any{"path": "x.txt"}, nil, dark, w80, []bool{false}, func(p *toolCardProbe) { p.CWD = dir })
	}
	add("read compact via file_path", "read", map[string]any{"file_path": "x/SKILL.md", "path": "AGENTS.md"}, nil, both, bothWidths, bothStates, nil)
	add("read compact with a result", "read", map[string]any{"path": "skills/demo/SKILL.md"}, ok("# Skill\n\nbody"), both, bothWidths, bothStates, nil)
	add("read compact with an error", "read", map[string]any{"path": "AGENTS.md"}, failed("ENOENT: no such file"), both, bothWidths, bothStates, nil)
	for _, page := range []string{"README.md", "docs/extensions.md", "docs/sub/page.md"} {
		pigPath := filepath.Join(docsRoot, strings.TrimPrefix(page, "docs/"))
		if page == "README.md" {
			pigPath = filepath.Join(docsRoot, "README.md")
		}
		for _, rng := range []map[string]any{{}, {"offset": 3, "limit": 4}} {
			args := map[string]any{"path": pigPath}
			for k, v := range rng {
				args[k] = v
			}
			add(fmt.Sprintf("read docs %s %v", page, rng), "read", args, nil, both, bothWidths, []bool{false}, func(p *toolCardProbe) { p.DocsPage = page })
		}
	}

	// read renderResult: nothing while collapsed unless the read failed; otherwise the output by the path's language.
	outputs := map[string]string{
		"empty":                 "",
		"one line":              "hello",
		"nine lines":            rows(9, "line"),
		"ten lines":             rows(10, "line"),
		"eleven lines":          rows(11, "line"),
		"twelve lines":          rows(12, "line"),
		"thirty lines":          rows(30, "line"),
		"trailing blank lines":  rows(3, "line") + "\n\n\n",
		"only newlines":         "\n\n\n",
		"leading blank lines":   "\n\n" + rows(2, "line"),
		"tabs":                  "\ttabbed\n a\tb\n",
		"carriage returns":      "one\r\ntwo\rthree\r\n",
		"ansi":                  "\x1b[31mred\x1b[0m plain\n\x1b[1;4mbold\x1b[0m",
		"control bytes":         "bin\x00\x07\x1b]0;title\x07end",
		"wide":                  "日本語のテキスト\n😀 e\u0301",
		"long line":             strings.Repeat("w", 150),
		"eleven with blank end": rows(11, "line") + "\n\n",
	}
	paths := []string{"notes.txt", "main.go", "README.md", "data.json", "noext", "", "a.TS", "x.unknownext"}
	for name, output := range ordered(outputs) {
		for _, path := range paths {
			args := map[string]any{"path": path}
			add(fmt.Sprintf("read result %s %q", name, path), "read", args, ok(output), dark, w80, bothStates, nil)
		}
	}
	for name, output := range ordered(map[string]string{"ten": rows(10, "e"), "eleven": rows(11, "e"), "thirty": rows(30, "e")}) {
		add("read error "+name+" in a highlighted path", "read", map[string]any{"path": "main.go"}, failed(output), both, bothWidths, bothStates, nil)
		add("read error "+name+" in a plain path", "read", map[string]any{"path": "notes.txt"}, failed(output), dark, w80, bothStates, nil)
	}
	add("read result with a numeric path", "read", map[string]any{"path": 5}, ok(rows(12, "n")), dark, w80, bothStates, nil)
	add("read result with file_path", "read", map[string]any{"file_path": "main.go", "path": "notes.txt"}, ok("package main\nfunc main() {}"), dark, w80, bothStates, nil)
	add("read result with an empty file_path", "read", map[string]any{"file_path": "", "path": "main.go"}, ok("package main"), dark, w80, bothStates, nil)
	add("read result with a null file_path", "read", map[string]any{"file_path": nil, "path": "main.go"}, ok("package main"), dark, w80, bothStates, nil)
	// Several text blocks join with a line break; an empty block is an empty line.
	blocks := func(texts ...string) *toolCardResult {
		result := &toolCardResult{}
		for _, text := range texts {
			result.Content = append(result.Content, cardText(text)[0])
		}
		return result
	}
	add("read result with two blocks", "read", map[string]any{"path": "notes.txt"}, blocks("first", "second"), dark, w80, bothStates, nil)
	add("read result with an empty block between", "read", map[string]any{"path": "notes.txt"}, blocks("first", "", "third"), dark, w80, bothStates, nil)
	add("read result with only empty blocks", "read", map[string]any{"path": "notes.txt"}, blocks("", ""), dark, w80, bothStates, nil)
	add("read result with a highlighted block pair", "read", map[string]any{"path": "main.go"}, blocks("package main", "func main() {}"), dark, w80, bothStates, nil)
	add("read result with no content", "read", map[string]any{"path": "notes.txt"}, &toolCardResult{Content: []map[string]any{}}, dark, w80, bothStates, nil)
	add("read partial result", "read", map[string]any{"path": "main.go"}, ok(rows(12, "p")), dark, w80, bothStates, nil)

	// read renderResult: the language of every extension of getLanguageFromPath's table, and the paths that fall outside it.
	for _, ext := range []string{"ts", "tsx", "js", "jsx", "mjs", "cjs", "py", "rb", "rs", "go", "java", "kt", "swift", "c", "h", "cpp", "cc", "cxx", "hpp", "cs", "php", "sh", "bash", "zsh", "fish", "ps1", "sql", "html", "htm", "css", "scss", "sass", "less", "json", "yaml", "yml", "toml", "xml", "md", "markdown", "dockerfile", "makefile", "cmake", "lua", "perl", "r", "scala", "clj", "ex", "exs", "erl", "hs", "ml", "vim", "graphql", "proto", "tf", "hcl", "TS", "Json", "GO"} {
		add("read language ."+ext, "read", map[string]any{"path": "dir/file." + ext}, ok("a = 1\n\t// \"b\" # c\n<tag attr='1'>x</tag>\n{ \"k\": [1, 2] }"), dark, w80, []bool{true}, nil)
	}
	for _, path := range []string{"Dockerfile", "Makefile", "makefile", ".gitignore", ".bashrc", "a.", "a..go", "dir.go/noext", "path.with.dots/noext", "x.tar.gz", "é.go", "İ.go", "a.go ", " a.go", "main.go\n"} {
		add(fmt.Sprintf("read language of %q", path), "read", map[string]any{"path": path}, ok("a = 1\n\"b\" # c"), dark, w80, []bool{true}, nil)
	}

	// read renderResult: every truncation warning.
	trunc := func(members map[string]any) *toolCardResult {
		details := map[string]any{"truncated": true, "truncatedBy": "lines", "totalLines": 5000, "totalBytes": 90000, "outputLines": 2000, "outputBytes": 50000, "lastLinePartial": false, "firstLineExceedsLimit": false}
		for k, v := range members {
			if v == nil {
				delete(details, k)
				continue
			}
			details[k] = v
		}
		return &toolCardResult{Content: cardText(rows(12, "t")), Details: map[string]any{"truncation": details}}
	}
	for name, members := range ordered(map[string]map[string]any{
		"by lines with limits":           {"maxLines": 2000, "maxBytes": 51200},
		"by lines without limits":        {},
		"by lines with a custom limit":   {"maxLines": 500},
		"by bytes with limits":           {"truncatedBy": "bytes", "maxLines": 2000, "maxBytes": 51200},
		"by bytes without limits":        {"truncatedBy": "bytes"},
		"by bytes with a custom limit":   {"truncatedBy": "bytes", "maxBytes": 1500},
		"by bytes limit of one mebibyte": {"truncatedBy": "bytes", "maxBytes": 1048576},
		"by bytes limit just under a KB": {"truncatedBy": "bytes", "maxBytes": 1023},
		"by bytes limit of 10.5 KB":      {"truncatedBy": "bytes", "maxBytes": 10752},
		"first line exceeds with limit":  {"firstLineExceedsLimit": true, "maxBytes": 51200},
		"first line exceeds no limit":    {"firstLineExceedsLimit": true},
		"first line beats lines":         {"firstLineExceedsLimit": true, "truncatedBy": "lines", "maxBytes": 2048},
		"unknown truncatedBy":            {"truncatedBy": "tokens"},
		"null truncatedBy":               {"truncatedBy": nil},
		"zero limits":                    {"maxLines": 0, "maxBytes": 0},
		"not truncated":                  {"truncated": false},
		"missing counts":                 {"outputLines": nil, "totalLines": nil},
	}) {
		add("read truncation "+name, "read", map[string]any{"path": "notes.txt"}, trunc(members), both, bothWidths, bothStates, nil)
	}
	add("read truncation null", "read", map[string]any{"path": "notes.txt"}, &toolCardResult{Content: cardText("x"), Details: map[string]any{"truncation": nil}}, dark, w80, bothStates, nil)
	add("read details without truncation", "read", map[string]any{"path": "notes.txt"}, &toolCardResult{Content: cardText("x"), Details: map[string]any{"other": 1}}, dark, w80, bothStates, nil)
	add("read truncation on an error", "read", map[string]any{"path": "main.go"}, &toolCardResult{Content: cardText("boom"), IsError: true, Details: map[string]any{"truncation": map[string]any{"truncated": true, "truncatedBy": "lines", "outputLines": 3, "totalLines": 9}}}, dark, w80, bothStates, nil)
	add("read truncation in a highlighted file", "read", map[string]any{"path": "main.go"}, &toolCardResult{Content: cardText(rows(30, "x := 1 //")), Details: map[string]any{"truncation": map[string]any{"truncated": true, "truncatedBy": "bytes", "outputLines": 30, "maxBytes": 4096}}}, dark, w80, bothStates, nil)

	// read renderResult: image blocks next to text (the images fallback line carries the type), with and without showing images.
	png := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	image := map[string]any{"type": "image", "data": png, "mimeType": "image/png"}
	for _, noImages := range []bool{false, true} {
		for name, content := range ordered(map[string][]map[string]any{
			"text and image":       {cardText("Read image file [image/png]")[0], image},
			"image only":           {image},
			"empty text and image": {cardText("")[0], image},
			"image then text":      {image, cardText("after")[0]},
			"two images":           {image, image},
			"unknown mime":         {{"type": "image", "data": "AAAA", "mimeType": "image/x-unknown"}},
		}) {
			add(fmt.Sprintf("read image %s noImages=%v", name, noImages), "read", map[string]any{"path": "pic.png"}, &toolCardResult{Content: content}, dark, wide, bothStates, func(p *toolCardProbe) { p.NoImages = noImages })
			add(fmt.Sprintf("read image error %s noImages=%v", name, noImages), "read", map[string]any{"path": "pic.png"}, &toolCardResult{Content: content, IsError: true}, dark, w80, bothStates, func(p *toolCardProbe) { p.NoImages = noImages })
		}
	}

	// write renderCall: the content argument and its lines.
	for name, args := range ordered(map[string]map[string]any{
		"no content":           {"path": "out.txt"},
		"null content":         {"path": "out.txt", "content": nil},
		"empty content":        {"path": "out.txt", "content": ""},
		"number content":       {"path": "out.txt", "content": 5},
		"bool content":         {"path": "out.txt", "content": false},
		"object content":       {"path": "out.txt", "content": map[string]any{"a": 1}},
		"array content":        {"path": "out.txt", "content": []any{"a"}},
		"one line":             {"path": "out.txt", "content": "hello"},
		"only a newline":       {"path": "out.txt", "content": "\n"},
		"only newlines":        {"path": "out.txt", "content": "\n\n\n"},
		"only carriage return": {"path": "out.txt", "content": "\r"},
		"nine lines":           {"path": "out.txt", "content": rows(9, "w")},
		"ten lines":            {"path": "out.txt", "content": rows(10, "w")},
		"eleven lines":         {"path": "out.txt", "content": rows(11, "w")},
		"twelve lines":         {"path": "out.txt", "content": rows(12, "w")},
		"thirty lines":         {"path": "out.txt", "content": rows(30, "w")},
		"ten and blank end":    {"path": "out.txt", "content": rows(10, "w") + "\n\n\n"},
		"eleven and blank end": {"path": "out.txt", "content": rows(11, "w") + "\n\n"},
		"blank lines inside":   {"path": "out.txt", "content": "a\n\n\nb\n\nc"},
		"tabs":                 {"path": "out.txt", "content": "\ttabbed\n a\tb"},
		"carriage returns":     {"path": "out.txt", "content": "one\r\ntwo\rthree\r\n\r\n"},
		"ansi":                 {"path": "out.txt", "content": "\x1b[31mred\x1b[0m\n\x1b[1mbold"},
		"wide":                 {"path": "out.txt", "content": "日本語のテキスト\n😀 e\u0301"},
		"long line":            {"path": "out.txt", "content": strings.Repeat("w", 150)},
		"no path":              {"content": "hello"},
		"empty path":           {"path": "", "content": "hello"},
		"number path":          {"path": 5, "content": "hello"},
		"file_path wins":       {"file_path": "main.go", "path": "out.txt", "content": "package main\nfunc main() {}"},
		"null file_path":       {"file_path": nil, "path": "main.go", "content": "package main"},
		"empty file_path":      {"file_path": "", "path": "main.go", "content": "package main"},
		"home path":            {"path": "~/out.txt", "content": "hello"},
		"nothing":              {},
	}) {
		add("write call "+name, "write", args, nil, both, bothWidths, bothStates, nil)
		add("write call with result "+name, "write", args, ok("Successfully wrote 12 bytes"), dark, w80, bothStates, nil)
	}
	goSource := "package main\n\nimport \"fmt\"\n\n// main prints.\nfunc main() {\n\tfmt.Println(\"hi\", 42)\n}\n"
	for _, path := range []string{"main.go", "a.TS", "README.md", "data.json", "noext", "x.unknownext", "Dockerfile", "dir.go/noext", "schema.graphql"} {
		for _, n := range []int{3, 10, 11, 40} {
			content := goSource
			if n > 8 {
				content = goSource + rows(n-8, "x := 1 //")
			}
			add(fmt.Sprintf("write call language %q %d lines", path, n), "write", map[string]any{"path": path, "content": content}, nil, dark, w80, bothStates, nil)
		}
	}
	for _, ext := range []string{"ts", "js", "py", "rb", "rs", "go", "java", "kt", "swift", "c", "cpp", "cs", "php", "sh", "fish", "ps1", "sql", "html", "css", "scss", "json", "yaml", "toml", "xml", "md", "lua", "hs", "proto", "tf"} {
		add("write call language ."+ext, "write", map[string]any{"path": "f." + ext, "content": "a = 1\n\t// \"b\" # c\n<tag attr='1'>x</tag>\n{ \"k\": [1, 2] }"}, nil, dark, w80, []bool{true}, nil)
	}

	// write renderResult: nothing on success; the error text of a failed write, text blocks joined by a line break, nothing for an empty one.
	for name, result := range ordered(map[string]*toolCardResult{
		"success":                   ok("Successfully wrote 12 bytes to out.txt"),
		"success with no text":      ok(""),
		"success with details":      {Content: cardText("done"), Details: map[string]any{"x": 1}},
		"error":                     failed("EACCES: permission denied"),
		"error with two blocks":     {Content: []map[string]any{cardText("first")[0], cardText("second")[0]}, IsError: true},
		"error with an empty one":   {Content: []map[string]any{cardText("first")[0], cardText("")[0], cardText("third")[0]}, IsError: true},
		"error with empty text":     failed(""),
		"error with no content":     {Content: []map[string]any{}, IsError: true},
		"error with an image only":  {Content: []map[string]any{{"type": "image", "data": png, "mimeType": "image/png"}}, IsError: true},
		"error with text and image": {Content: []map[string]any{cardText("failed")[0], {"type": "image", "data": png, "mimeType": "image/png"}}, IsError: true},
		"error with ansi":           failed("\x1b[31mred\x1b[0m boom"),
		"error with long text":      failed(strings.Repeat("word ", 40)),
		"error with many lines":     failed(rows(30, "err")),
		"error with tabs and CR":    failed("a\tb\r\nc"),
		"error with wide text":      failed("日本語のエラー 😀"),
	}) {
		add("write result "+name, "write", map[string]any{"path": "out.txt", "content": rows(3, "w")}, result, both, bothWidths, bothStates, nil)
	}
	add("write partial result", "write", map[string]any{"path": "out.txt", "content": "x"}, failed("partial failure"), dark, w80, bothStates, nil)

	// Path links: a terminal with hyperlink support links the path in the call header, and a path whose file URL cannot be named makes the call renderer
	// throw, which draws the call fallback (read, write, edit and ls resolve it; grep and find only shorten it).
	for _, path := range []string{"notes.txt", "/abs/dir/f.go", "~/f.txt", "a b#c.txt", "file:///tmp/x%20y", "file://localhost/tmp/x", "file://host/x", "file:///a%2Fb", "file:///a%00b", "日本語.txt", "SKILL.md", "AGENTS.md"} {
		for _, tool := range []string{"read", "write", "edit", "ls", "grep", "find"} {
			// Pi's edit renderCall starts the preview of the edit before it draws the header, and a file URL it cannot resolve rejects that
			// promise with no handler, which ends Pi's process: there is no card to compare.
			if tool == "edit" && (path == "file://host/x" || path == "file:///a%2Fb") {
				continue
			}
			args := map[string]any{"path": path}
			var result *toolCardResult
			switch tool {
			case "write":
				args["content"] = "package main\n"
			case "edit":
				args["edits"] = []any{map[string]any{"oldText": "a", "newText": "b"}}
			case "grep", "find":
				args["pattern"] = "needle"
			}
			for _, withResult := range []bool{false, true} {
				if withResult {
					result = ok("done")
				}
				add(fmt.Sprintf("%s linked %q result=%v", tool, path, withResult), tool, args, result, dark, w80, bothStates, func(p *toolCardProbe) { p.Hyperlinks = true })
			}
		}
	}
	return probes, names
}

// The read and write tool cards (renderers/read.ts and renderers/write.ts, with render-utils.ts' path, range and output helpers) against pinned Pi over
// readWriteCardProbes. Rows are compared byte for byte: colours, backgrounds and padding included.
func TestReadWriteCardsMatchPi(t *testing.T) {
	probes, names := readWriteCardProbes(t)
	compareToolCards(t, probes, names)
}

// TestReadWriteCardsProbeDump prints the corpus for the Pi side of the tools/27 parity scenario.
func TestReadWriteCardsProbeDump(t *testing.T) {
	probes, _ := readWriteCardProbes(t)
	dumpToolCardProbes(t, probes)
}

// TestReadWriteCardsParity prints Pig's rows, one JSON line per probe, for the tools/27 parity scenario.
func TestReadWriteCardsParity(t *testing.T) {
	// The Pi side of the scenario loads every highlight.js language before it draws, as compareToolCards does for the Go oracle tests.
	tui.LoadAllHighlightLanguages()
	probes, _ := readWriteCardProbes(t)
	printToolCardRows(t, renderBuiltinToolCards(t, probes))
}

// ordered ranges a map in key order, so the corpus of the Pi side and the PiG side of a scenario is the same list.
func ordered[V any](m map[string]V) iter.Seq2[string, V] {
	return func(yield func(string, V) bool) {
		for _, key := range slices.Sorted(maps.Keys(m)) {
			if !yield(key, m[key]) {
				return
			}
		}
	}
}

// dumpToolCardProbes prints probes for the Pi side of a tool-card parity scenario.
func dumpToolCardProbes(t *testing.T, probes []toolCardProbe) {
	t.Helper()
	line, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("toolcard-probes:%s\n", line)
}

// printToolCardRows prints the rows of each probe as one JSON line for a tool-card parity scenario.
func printToolCardRows(t *testing.T, cards [][]string) {
	t.Helper()
	for _, rows := range cards {
		var line strings.Builder
		encoder := json.NewEncoder(&line)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(rows); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("toolcard-observation:%s", line.String())
	}
}
