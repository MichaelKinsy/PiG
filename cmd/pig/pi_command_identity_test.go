package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// piCommandInstruction matches text that tells the user to run a `pi` command line, such as "run `pi config`" or
// `"pi -ne"`: the command opens a quoted span or follows run/use/using/try. Prose about Pi itself ("pi has joined
// Earendil", "upstream pi install path") does not match.
var piCommandInstruction = regexp.MustCompile("(?:^|[\"'`(]|\\b(?:[Rr]un|[Uu]se|using|[Tt]ry) )pi (?:config|install|remove|uninstall|update|list|login|logout|mcp|-ne|-e|-c|-r|-p|--[a-z][a-z-]*)\\b")

// D2: PiG's command is `pig`, so a message that tells the user to run a command never names `pi`. Pi's own texts say
// `pi config` (resource-loader.ts omitReplacedExtensions) and `pi -ne` (main.ts EXTENSION_LOAD_FAILURE_HINT); a port that
// copies such a literal tells a PiG user to run another program. The guard reads every string literal of the production
// Go sources; comments and tests may name Pi's commands.
func TestProductionStringsNeverInstructAPiCommand(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	fset := token.NewFileSet()
	var scanned int
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".upstream", "node_modules", "testdata", "tmp", "bin", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		scanned++
		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, isLiteral := node.(*ast.BasicLit)
			if !isLiteral || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil {
				text = literal.Value
			}
			if match := piCommandInstruction.FindString(text); match != "" {
				relative, _ := filepath.Rel(root, path)
				found = append(found, relative+":"+strconv.Itoa(fset.Position(literal.Pos()).Line)+": "+strings.TrimSpace(match))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The scan must reach the files that build the warning and the hint, or an empty result proves nothing.
	if scanned < 100 {
		t.Fatalf("scanned %d production Go files under %s, want the whole tree", scanned, root)
	}
	if len(found) > 0 {
		t.Fatalf("production strings tell the user to run `pi` instead of `pig` (D2):\n%s", strings.Join(found, "\n"))
	}
}

func TestPiCommandInstructionPattern(t *testing.T) {
	for text, want := range map[string]bool{
		"run `pi config` and make sure":                  true,
		`Hint: Start without extensions using "pi -ne".`: true,
		"not logged in (run `pi login github-copilot`)":  true,
		"pi --help":                      true,
		"run `pig config` and make sure": false,
		`Hint: Start without extensions using "pig -ne".`: false,
		"pi has joined Earendil":                          false,
		"running from upstream pi install path":           false,
		"Run pi update to upgrade":                        true,
		"the api config is invalid":                       false,
	} {
		if got := piCommandInstruction.MatchString(text); got != want {
			t.Errorf("match(%q) = %t, want %t", text, got, want)
		}
	}
}
