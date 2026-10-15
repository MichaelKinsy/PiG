package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/migrations.ts

// runMigrations over fixture trees: legacy oauth.json and settings.json apiKeys into auth.json, session files at the agent root into their cwd
// directory, tools/ binaries into bin/, commands/ into prompts/, the keybinding rewrite, and the deprecation warnings. The pinned runMigrations and PiG's
// RunMigrations run on the same tree (file contents are Latin-1 strings so the bytes are exact); the returned values and the whole resulting tree
// (paths, contents, the auth.json mode; Pi's project .pi is ConfigDirName here) must be equal. Fixture paths are relative to a base directory holding agent/ and project/.
func TestRunMigrationsTreeMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(61))
	pick := func(options ...string) string { return options[rng.Intn(len(options))] }
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	var fixtures []map[string]*string
	put := func(f map[string]*string, path, content string) { f[path] = &content }
	for range 300 {
		f := map[string]*string{}
		switch rng.Intn(6) {
		case 1:
			put(f, "agent/oauth.json", fmt.Sprintf(`{"anthropic":{"refresh":"r","access":"a","expires":%d},"z":{"refresh":%s,"extra":{"b":1,"a":2}},"10":{"access":"x"}}`, rng.Intn(1e9), quote(pick("r", "<&>", "\u00e2\u0080\u00a8"))))
		case 2:
			put(f, "agent/oauth.json", "{broken")
		case 3:
			put(f, "agent/oauth.json", "\u00ef\u00bb\u00bf"+`{"p":{"refresh":"r","type":"other"}}`)
		case 4:
			// Object.entries and the spread of a non-object: array elements, a string's code units under their indexes (integer-like names before "type"), null spreads nothing.
			put(f, "agent/oauth.json", pick(`[1,2]`, `"xy"`, `{"p":"ab","q":[3,"c"],"r":null,"s":{"type":"t","0":"z"}}`))
		}
		switch rng.Intn(6) {
		case 1:
			put(f, "agent/settings.json", fmt.Sprintf(`{"theme":"dark","apiKeys":{"openai":%s,"anthropic":"k2","bad":5,"z":"z"},"packages":["a"]}`, quote(pick("sk-1", "<&>", "a b"))))
		case 2:
			put(f, "agent/settings.json", `{"apiKeys":{},"x":1.50}`)
		case 3:
			put(f, "agent/settings.json", `{"apiKeys":"no"}`)
		case 4:
			put(f, "agent/settings.json", "nope")
		}
		if rng.Intn(6) == 0 {
			put(f, "agent/auth.json", `{"existing":{"type":"api_key","key":"k"}}`)
		}
		for i := range rng.Intn(4) {
			header := fmt.Sprintf(`{"type":"session","version":3,"id":"s%d","timestamp":"t","cwd":%s}`, i, quote(pick("/work/a", "/work/b", `C:\\Users\\x`, "/", "", "relative/dir", `\\srv\\share`)))
			if rng.Intn(5) == 0 {
				header = pick("", "   ", `{"type":"other","cwd":"/x"}`, "not json", `{"type":"session"}`)
			}
			put(f, fmt.Sprintf("agent/s%d.jsonl", i), header+"\n{\"type\":\"message\"}\n")
		}
		if rng.Intn(5) == 0 {
			put(f, "agent/sessions/--work-a--/s0.jsonl", "existing\n")
		}
		if rng.Intn(4) == 0 {
			put(f, "agent/commands/a.md", "cmd")
			if rng.Intn(3) == 0 {
				put(f, "agent/prompts/b.md", "prompt")
			}
		}
		if rng.Intn(4) == 0 {
			put(f, "project/.pi/commands/p.md", "cmd")
		}
		if rng.Intn(5) == 0 {
			f["agent/hooks"] = nil
		}
		if rng.Intn(4) == 0 {
			put(f, "agent/tools/fd", "fd")
			put(f, "agent/tools/rg.exe", "rg")
			if rng.Intn(2) == 0 {
				put(f, "agent/bin/fd", "newfd")
			}
			if rng.Intn(2) == 0 {
				put(f, "agent/tools/"+pick("custom.sh", ".DS_Store", "FD", "RG.EXE"), "x")
			}
		}
		if rng.Intn(5) == 0 {
			put(f, "project/.pi/tools/mytool.ts", "x")
			put(f, "project/.pi/hooks/h.ts", "x")
		}
		switch rng.Intn(6) {
		case 1:
			put(f, "agent/keybindings.json", `{"cursorUp":"up","selectConfirm":["enter","ctrl+m"],"app.exit":"ctrl+q","zzz":"x","tui.select.up":"k","selectUp":"j"}`)
		case 2:
			put(f, "agent/keybindings.json", "\u00ef\u00bb\u00bf"+`{"expandTools":"ctrl+o","custom.thing":["a"]}`)
		case 3:
			put(f, "agent/keybindings.json", `{"tui.editor.cursorUp":"up"}`)
		case 4:
			put(f, "agent/keybindings.json", `[1]`)
		}
		fixtures = append(fixtures, f)
	}
	compareMigrationTrees(t, fixtures)
}

// runMigrations over names that differ only in case from the ones it probes, and fixtures holding two such spellings of one entry. Every step probes fixed names with existsSync and renames, removes or rewrites the probed path (migrations.ts:21-216), and migrateSessionsFromAgentRoot and checkDeprecatedExtensionDirs read the directory: on a case-insensitive filesystem (APFS, NTFS) OAuth.json, Settings.json, AUTH.JSON, COMMANDS/, PROMPTS/, HOOKS/, KeyBindings.json, tools/Fd and the sessions directory --WORK-A-- are the probed entries, S0.JSONL does not end in ".jsonl", and two spellings written in turn are one file holding the later content; on a case-sensitive filesystem each is an entry of its own.
func TestRunMigrationsCaseVariantNamesMatchPi(t *testing.T) {
	s := func(content string) *string { return &content }
	header := func(cwd string) *string {
		return s(`{"type":"session","version":3,"id":"s","timestamp":"t","cwd":"` + cwd + `"}` + "\n{\"type\":\"message\"}\n")
	}
	compareMigrationTrees(t, []map[string]*string{
		{"agent/tools/FD": s("x"), "agent/tools/fd": s("fd"), "agent/tools/RG.EXE": s("x"), "agent/tools/rg.exe": s("rg")},
		{"agent/tools/FD": s("x"), "agent/tools/fd": s("fd"), "agent/bin/FD": s("newfd")},
		{"agent/tools/Fd": s("fd"), "agent/tools/RG": s("rg"), "agent/tools/Custom.sh": s("x")},
		{"agent/COMMANDS/a.md": s("cmd"), "project/.pi/Commands/p.md": s("cmd")},
		{"agent/commands/a.md": s("cmd"), "agent/PROMPTS/b.md": s("prompt")},
		{"agent/AUTH.JSON": s(`{"existing":{"type":"api_key","key":"k"}}`), "agent/oauth.json": s(`{"p":{"refresh":"r"}}`)},
		{"agent/OAuth.json": s(`{"p":{"refresh":"r"}}`), "agent/Settings.json": s(`{"theme":"dark","apiKeys":{"q":"k"}}`)},
		{"agent/OAUTH.JSON": s(`{"a":{"refresh":"1"}}`), "agent/oauth.json": s(`{"b":{"refresh":"2"}}`), "agent/SETTINGS.JSON": s(`{"apiKeys":{"c":"k"}}`), "agent/settings.json": s(`{"apiKeys":{"d":"k"}}`)},
		{"agent/HOOKS": nil, "project/.pi/Hooks/h.ts": s("x"), "project/.pi/TOOLS/t.ts": s("x")},
		{"agent/KeyBindings.json": s(`{"cursorUp":"up"}`)},
		{"agent/KEYBINDINGS.JSON": s(`{"cursorUp":"up"}`), "agent/keybindings.json": s(`{"tui.editor.cursorUp":"k"}`)},
		{"agent/s0.jsonl": header("/Work/A"), "agent/s1.jsonl": header("/work/a"), "agent/sessions/--WORK-A--/s1.jsonl": s("existing\n"), "agent/s2.JSONL": header("/work/b")},
		{"agent/S0.JSONL": header("/b"), "agent/s0.jsonl": header("/a")},
	})
}

// compareMigrationTrees runs the pinned runMigrations and RunMigrations on each fixture and reports the fixtures whose results or trees differ.
func compareMigrationTrees(t *testing.T, fixtures []map[string]*string) {
	t.Helper()
	input, err := json.Marshal(fixtures)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/migrations_tree.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Providers []string
		Warnings  []string
		Tree      map[string]*string
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, f := range fixtures {
		base := t.TempDir()
		agentDir, cwd := filepath.Join(base, "agent"), filepath.Join(base, "project")
		for _, dir := range []string{agentDir, cwd} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		// The oracle writes the entries in Object.entries order of json.Marshal's sorted keys; on a case-insensitive filesystem two spellings of one name are one entry holding the later write, so PiG's tree is the oracle's only in the same order.
		for _, path := range slices.Sorted(maps.Keys(f)) {
			content := f[path]
			target := filepath.Join(base, strings.Replace(path, "project/.pi/", "project/"+ConfigDirName()+"/", 1))
			if content == nil {
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, latin1ToBytes(*content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		providers, warnings, err := RunMigrations(cwd, agentDir)
		if err != nil {
			t.Fatalf("fixture %d: %v", i, err)
		}
		tree := map[string]*string{}
		_ = filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			rel, _ := filepath.Rel(base, path)
			rel = filepath.ToSlash(rel)
			if rel == "." {
				return nil
			}
			// PiG names the project configuration directory by ConfigDirName; Pi's is .pi.
			if rel == "project/"+ConfigDirName() || strings.HasPrefix(rel, "project/"+ConfigDirName()+"/") {
				rel = "project/.pi" + strings.TrimPrefix(rel, "project/"+ConfigDirName())
			}
			if entry.IsDir() {
				tree[rel+"/"] = nil
				return nil
			}
			data, _ := os.ReadFile(path)
			text := string(bytesToLatin1Runes(data))
			if rel == "agent/auth.json" {
				info, _ := entry.Info()
				text += fmt.Sprintf(" mode=%o", info.Mode().Perm())
			}
			tree[rel] = &text
			return nil
		})
		// Pi lists the tree relative to the base of its own run; both bases hold the same relative paths.
		if providers == nil {
			providers = []string{}
		}
		if warnings == nil {
			warnings = []string{}
		}
		if want[i].Providers == nil {
			want[i].Providers = []string{}
		}
		if want[i].Warnings == nil {
			want[i].Warnings = []string{}
		}
		if !reflect.DeepEqual(providers, want[i].Providers) || !reflect.DeepEqual(warnings, want[i].Warnings) || !reflect.DeepEqual(tree, want[i].Tree) {
			if failures++; failures <= 3 {
				t.Errorf("fixture %d: %v\n PiG providers %q warnings %q\n Pi  providers %q warnings %q\n%s", i, fixturePaths(f), providers, warnings, want[i].Providers, want[i].Warnings, migrationTreeDiff(tree, want[i].Tree))
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d fixtures differ from Pi", failures, len(fixtures))
	}
}

func latin1ToBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		out = append(out, byte(r))
	}
	return out
}

func bytesToLatin1Runes(data []byte) []rune {
	out := make([]rune, len(data))
	for i, b := range data {
		out[i] = rune(b)
	}
	return out
}

func fixturePaths(f map[string]*string) []string {
	var keys []string
	for key := range f {
		keys = append(keys, key)
	}
	return keys
}

func migrationTreeDiff(got, want map[string]*string) string {
	var out []string
	for path, text := range got {
		other, ok := want[path]
		switch {
		case !ok:
			out = append(out, "  only in PiG: "+path)
		case (text == nil) != (other == nil) || (text != nil && *text != *other):
			out = append(out, fmt.Sprintf("  differs: %s\n   PiG %.300q\n   Pi  %.300q", path, migrationText(text), migrationText(other)))
		}
	}
	for path := range want {
		if _, ok := got[path]; !ok {
			out = append(out, "  only in Pi: "+path)
		}
	}
	return strings.Join(out, "\n")
}

func migrationText(s *string) string {
	if s == nil {
		return "<dir>"
	}
	return *s
}
