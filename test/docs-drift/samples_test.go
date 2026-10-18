package docsdrift

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"
)

// Every fenced code sample in the user documentation is extracted and checked by the strongest means that is safe to run in a test: JSON, YAML and TOML parse, Go parses and its imports resolve, JavaScript and Python are syntax-checked by their own interpreters, and a `pig` command line is checked against the real binary (see commands_test.go). A sample that cannot be checked says why with a `<!-- sample: skip: <reason> -->` line before its fence. A fence language this table does not know fails, so a new kind of sample is a decision rather than an omission.

type sampleCheck func(t *testing.T, s sample)

var sampleChecks = map[string]sampleCheck{
	"json":       checkJSON,
	"jsonc":      checkJSON,
	"yaml":       checkYAML,
	"yml":        checkYAML,
	"toml":       checkTOML,
	"go":         checkGo,
	"js":         checkNode,
	"javascript": checkNode,
	"python":     checkPython,
	"py":         checkPython,
	// Shell samples hold placeholders such as <path>, so bash cannot parse them; their `pig` lines are checked against the binary by TestDocumentedPigCommandsAreValid.
	"bash":    nil,
	"sh":      nil,
	"shell":   nil,
	"console": nil,
	// Output, prose and formats with no parser available to the docs job: the block's text is the sample.
	"text":     nil,
	"":         nil,
	"markdown": nil,
	// Diagrams copied from Pi's docs; the site renders them and no parser runs in the docs job.
	"mermaid":    nil,
	"md":         nil,
	"tmux":       nil,
	"dockerfile": nil,
	"go.mod":     nil,
	"lua":        nil,
	"powershell": nil,
	"ini":        nil, // a llama.cpp router preset (nvidia-rtx-llama-cpp.md); no INI parser in the docs job, and llama-server owns the format
	"rust":       nil, // no Rust parser in the docs job; the SDK's own tests compile the SDK
	"ts":         checkTypeScript,
	"typescript": checkTypeScript,
}

func TestEverySampleIsChecked(t *testing.T) {
	counts := map[string]int{}
	for _, s := range allSamples(t) {
		check, known := sampleChecks[s.Lang]
		if !known {
			t.Errorf("%s:%d: fence language %q has no entry in sampleChecks; add one, with a checker or a reason it has none", s.Src, s.Line, s.Lang)
			continue
		}
		counts[s.Lang]++
		if s.Skip != "" || check == nil {
			continue
		}
		t.Run(s.Src+":"+itoa(s.Line), func(t *testing.T) { check(t, s) })
	}
	if counts["json"] < 50 || counts["bash"] < 100 {
		t.Fatalf("sample counts %v are too low; the extractor lost blocks", counts)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// stripJSONC removes comments and trailing commas, which the docs use in examples that Pig reads as JSONC-style, and leaves string contents alone.
func stripJSONC(src string) string {
	var out strings.Builder
	inString, escaped := false, false
	runes := []rune(src)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inString {
			out.WriteRune(r)
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inString = false
			}
			continue
		}
		switch {
		case r == '"':
			inString = true
			out.WriteRune(r)
		case r == '/' && i+1 < len(runes) && runes[i+1] == '/':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			out.WriteRune('\n')
		case r == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i += 2
			for i+1 < len(runes) && (runes[i] != '*' || runes[i+1] != '/') {
				i++
			}
			i++
		default:
			out.WriteRune(r)
		}
	}
	return trailingComma.ReplaceAllString(out.String(), "$1")
}

var (
	elidedObject = regexp.MustCompile(`\{\s*\.\.\.[^{}\n]*\}`)
	elidedArray  = regexp.MustCompile(`\[\s*\.\.\.[^\[\]\n]*\]`)
	elidedLine   = regexp.MustCompile(`(?m)^\s*\.\.\.[^\n]*$`)
)

// decodeJSONStream reports whether every value of a JSON text stream decodes.
func decodeJSONStream(body string) error {
	dec := json.NewDecoder(strings.NewReader(body))
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			if err.Error() == "EOF" {
				return nil
			}
			return err
		}
	}
}

// checkJSON parses a JSON sample after removing comments and trailing commas and replacing elided parts (`{ ... }`, a line of `...`) with empty values. A sample that is the members of an object, as in an excerpt of package.json, parses inside braces.
func checkJSON(t *testing.T, s sample) {
	body := stripJSONC(s.Body)
	body = elidedObject.ReplaceAllString(body, "{}")
	body = elidedArray.ReplaceAllString(body, "[]")
	body = elidedLine.ReplaceAllString(body, "")
	err := decodeJSONStream(body)
	if err != nil && decodeJSONStream("{"+trailingComma.ReplaceAllString(strings.TrimRight(body, ", \n\t"), "$1")+"}") == nil {
		return
	}
	if err != nil {
		t.Errorf("%s:%d: the JSON sample does not parse: %v\n%s", s.Src, s.Line, err, s.Body)
	}
}

func checkYAML(t *testing.T, s sample) {
	dec := yaml.NewDecoder(strings.NewReader(s.Body))
	for {
		var v any
		if err := dec.Decode(&v); err != nil {
			if err.Error() != "EOF" {
				t.Errorf("%s:%d: the YAML sample does not parse: %v", s.Src, s.Line, err)
			}
			return
		}
	}
}

func checkTOML(t *testing.T, s sample) {
	var v map[string]any
	if _, err := toml.Decode(s.Body, &v); err != nil {
		t.Errorf("%s:%d: the TOML sample does not parse: %v", s.Src, s.Line, err)
	}
}

// checkGo parses a Go sample as a file, then as declarations, then as statements. A sample that is a fragment with elided parts uses `...`, which no wrapping parses, and says so with a skip line.
func checkGo(t *testing.T, s sample) {
	fset := token.NewFileSet()
	attempts := []string{s.Body, "package p\n" + s.Body, "package p\nfunc _() {\n" + s.Body + "\n}"}
	var first error
	for _, src := range attempts {
		file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
		if err == nil {
			checkGoImports(t, s, file.Imports)
			return
		}
		if first == nil {
			first = err
		}
	}
	t.Errorf("%s:%d: the Go sample does not parse: %v", s.Src, s.Line, first)
}

func runTool(t *testing.T, timeout time.Duration, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return out.String(), os.ErrDeadlineExceeded
	}
}

func checkNode(t *testing.T, s sample) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	path := filepath.Join(t.TempDir(), "sample.mjs")
	// A Codemode script is the body of an async function: it awaits and returns at the top level.
	body := s.Body
	if strings.HasSuffix(s.Src, "codemode.md") || strings.HasSuffix(s.Src, "models.md") {
		body = "async function sample() {\n" + body + "\n}\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runTool(t, 30*time.Second, node, "--check", path); err != nil {
		t.Errorf("%s:%d: node rejects the JavaScript sample: %v\n%s", s.Src, s.Line, err, out)
	}
}

func checkPython(t *testing.T, s sample) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	path := filepath.Join(t.TempDir(), "sample.py")
	if err := os.WriteFile(path, []byte(s.Body), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runTool(t, 30*time.Second, py, "-c", "import sys; compile(open(sys.argv[1]).read(), sys.argv[1], 'exec')", path); err != nil {
		t.Errorf("%s:%d: python rejects the sample: %v\n%s", s.Src, s.Line, err, out)
	}
}

var (
	goRootOnce sync.Once
	goRootPath string
)

// goRoot returns the root of the Go installation that runs the tests, as `go env GOROOT` reports it.
func goRoot(t *testing.T) string {
	t.Helper()
	goRootOnce.Do(func() {
		out, err := exec.Command("go", "env", "GOROOT").Output()
		if err != nil {
			return
		}
		goRootPath = strings.TrimSpace(string(out))
	})
	if goRootPath == "" {
		t.Fatal("go env GOROOT failed")
	}
	return goRootPath
}

const modulePath = "github.com/MichaelKinsy/PiG"

var trailingComma = regexp.MustCompile(`,(\s*[}\]])`)

// checkGoImports requires each import of a sample to resolve: a standard library package, a package of this module that exists in the tree, or a module that go.mod requires.
func checkGoImports(t *testing.T, s sample, imports []*ast.ImportSpec) {
	t.Helper()
	for _, imp := range imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Errorf("%s:%d: bad import %s", s.Src, s.Line, imp.Path.Value)
			continue
		}
		switch {
		case !strings.Contains(strings.SplitN(path, "/", 2)[0], "."):
			if _, err := os.Stat(filepath.Join(goRoot(t), "src", filepath.FromSlash(path))); err != nil {
				t.Errorf("%s:%d: %q is not a standard library package", s.Src, s.Line, path)
			}
		case path == modulePath || strings.HasPrefix(path, modulePath+"/"):
			dir := filepath.Join(repoRoot, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(path, modulePath), "/")))
			if matches, _ := filepath.Glob(filepath.Join(dir, "*.go")); len(matches) == 0 {
				t.Errorf("%s:%d: the import %q has no package in this repository", s.Src, s.Line, path)
			}
		default:
			if !requiredModule(path) {
				t.Errorf("%s:%d: the import %q is not provided by any go.mod of this repository", s.Src, s.Line, path)
			}
		}
	}
}

// requiredModule reports whether some go.mod of the repository requires a module that provides path.
func requiredModule(path string) bool {
	mods, _ := filepath.Glob(filepath.Join(repoRoot, "go.mod"))
	more, _ := filepath.Glob(filepath.Join(repoRoot, "*", "*", "go.mod"))
	for _, f := range append(mods, more...) {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && !strings.HasPrefix(line, "//") && (path == fields[0] || strings.HasPrefix(path, fields[0]+"/")) {
				return true
			}
			if len(fields) >= 3 && fields[0] == "require" && (path == fields[1] || strings.HasPrefix(path, fields[1]+"/")) {
				return true
			}
		}
	}
	return false
}

// stripScript removes TypeScript types with Node's own stripper, which fails on a syntax error, then syntax-checks what is left.
const stripScript = `
const { stripTypeScriptTypes } = require("node:module");
const fs = require("node:fs");
const src = fs.readFileSync(process.argv[1], "utf8");
fs.writeFileSync(process.argv[2], stripTypeScriptTypes(src));
`

func checkTypeScript(t *testing.T, s sample) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	in, out := filepath.Join(dir, "sample.ts"), filepath.Join(dir, "sample.mjs")
	body := s.Body
	if strings.HasSuffix(s.Src, "codemode.md") {
		body = "async function sample() {\n" + body + "\n}\n"
	}
	if err := os.WriteFile(in, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if o, err := runTool(t, 30*time.Second, node, "-e", stripScript, in, out); err != nil {
		if strings.Contains(o, "stripTypeScriptTypes is not a function") || strings.Contains(o, "is not a function") {
			t.Skip("this Node has no module.stripTypeScriptTypes")
		}
		t.Errorf("%s:%d: Node rejects the TypeScript sample: %v\n%s", s.Src, s.Line, err, o)
		return
	}
	if o, err := runTool(t, 30*time.Second, node, "--check", out); err != nil {
		t.Errorf("%s:%d: node rejects the stripped TypeScript sample: %v\n%s", s.Src, s.Line, err, o)
	}
}
