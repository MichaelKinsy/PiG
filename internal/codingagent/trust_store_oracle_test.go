package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/trust-manager.ts ProjectTrustStore. A trust.json that is corrupted, holds wrong-typed values or is missing gives the same decisions, nearest entries, error messages and final bytes through get, getEntry, set and setMany.
func TestProjectTrustStoreMatchesPi(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	dirs := []string{"p", "p/a", "p/a/b", "p/c", "q", "q/r/s"}
	paths := []string{"p", "p/a", "p/a/b", "p/a/b/deep", "p/c", "q", "q/r/s", "q/r/s/t", "other", "p/x y", "p/é", "p/a/../c", "./q/", "p//a"}
	files := []string{
		"", "{}", " ", "\ufeff{}", "null", "[]", "5", `"s"`, "{", `{"a":}`, `{"$BASE/p":true}`, `{"$BASE/p":false,"$BASE/p/a":true}`, `{"$BASE/p/a/b":null}`, `{"$BASE/p":"yes"}`, `{"$BASE/p":1}`, `{"$BASE/p":[]}`,
		`{"$BASE/p":{}}`, `{"$BASE/q":true,"$BASE/p":true,"/zzz":false,"/aaa":null}`, `{"10":true,"2":false,"$BASE/p":true}`, `{"$BASE/p":true,"$BASE/p":false}`, "\ufeff" + `{"$BASE/q/r/s":false}`, `{"é":true,"z":false}`,
	}
	type op map[string]any
	type script struct {
		Initial *string  `json:"initial"`
		Dirs    []string `json:"dirs"`
		Ops     []op     `json:"ops"`
	}
	decision := func() any { return []any{true, false, nil}[rng.Intn(3)] }
	var scripts []script
	for range 800 {
		s := script{Dirs: dirs}
		if rng.Intn(8) != 0 {
			text := pick(files)
			s.Initial = &text
		}
		for range 1 + rng.Intn(6) {
			switch rng.Intn(5) {
			case 0:
				s.Ops = append(s.Ops, op{"op": "get", "path": pick(paths)})
			case 1:
				s.Ops = append(s.Ops, op{"op": "entry", "path": pick(paths)})
			case 2:
				s.Ops = append(s.Ops, op{"op": "set", "path": pick(paths), "decision": decision()})
			default:
				var updates []op
				for range 1 + rng.Intn(3) {
					updates = append(updates, op{"path": pick(paths), "decision": decision()})
				}
				s.Ops = append(s.Ops, op{"op": "setMany", "updates": updates})
			}
		}
		scripts = append(scripts, s)
	}
	input, _ := json.Marshal(scripts)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/trust_store.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []struct {
		Outcomes []string
		Text     *string
	}
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var differences []string
	for i, s := range scripts {
		dir := filepath.Join(base, fmt.Sprint(i))
		agentDir := filepath.Join(dir, "agent")
		_ = os.MkdirAll(agentDir, 0o755)
		for _, sub := range s.Dirs {
			_ = os.MkdirAll(filepath.Join(dir, sub), 0o755)
		}
		if s.Initial != nil {
			_ = os.WriteFile(filepath.Join(agentDir, "trust.json"), []byte(strings.ReplaceAll(*s.Initial, "$BASE", strings.ReplaceAll(dir, `\`, `\\`))), 0o644)
		}
		store := NewProjectTrustStore(agentDir)
		toDecision := func(v any) ProjectTrustDecision {
			if b, ok := v.(bool); ok {
				return &b
			}
			return nil
		}
		escapedDir := strings.ReplaceAll(dir, `\`, `\\`) // the path as it reads inside trust.json text on Windows
		fix := func(text string) string {
			return strings.ReplaceAll(strings.ReplaceAll(text, escapedDir, "$BASE"), dir, "$BASE")
		}
		var got []string
		for _, o := range s.Ops {
			path := dir + "/" + fmt.Sprint(o["path"])
			switch o["op"] {
			case "get":
				decision, gerr := store.Get(path)
				if gerr != nil {
					got = append(got, "error "+fix(gerr.Error()))
				} else {
					encoded, _ := json.Marshal(decision)
					got = append(got, string(encoded))
				}
			case "entry":
				entry, gerr := store.GetEntry(path)
				switch {
				case gerr != nil:
					got = append(got, "error "+fix(gerr.Error()))
				case entry == nil:
					got = append(got, "null")
				default:
					encoded, _ := json.Marshal(struct {
						Path     string `json:"path"`
						Decision bool   `json:"decision"`
					}{fix(entry.Path), entry.Decision})
					got = append(got, string(encoded))
				}
			case "set":
				if serr := store.Set(path, toDecision(o["decision"])); serr != nil {
					got = append(got, "error "+fix(serr.Error()))
				} else {
					got = append(got, "ok")
				}
			default:
				var updates []ProjectTrustUpdate
				for _, u := range o["updates"].([]op) {
					updates = append(updates, ProjectTrustUpdate{Path: dir + "/" + fmt.Sprint(u["path"]), Decision: toDecision(u["decision"])})
				}
				if serr := store.SetMany(updates); serr != nil {
					got = append(got, "error "+fix(serr.Error()))
				} else {
					got = append(got, "ok")
				}
			}
		}
		raw, readErr := os.ReadFile(filepath.Join(agentDir, "trust.json"))
		text := fix(string(raw))
		wantText := "<none>"
		if want[i].Text != nil {
			wantText = *want[i].Text
		}
		if readErr != nil {
			text = "<none>"
		}
		if strings.Join(got, "\n") != strings.Join(want[i].Outcomes, "\n") || text != wantText {
			differences = append(differences, fmt.Sprintf("script %d: initial %q ops %v\n  go: %q file %q\n  pi: %q file %q", i, deref3(s.Initial), s.Ops, got, text, want[i].Outcomes, wantText))
		}
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 8 {
			shown = shown[:8]
		}
		t.Errorf("%d of %d scripts differ from Pi:\n%s", len(differences), len(scripts), strings.Join(shown, "\n"))
	}
}

func deref3(s *string) string {
	if s == nil {
		return "<absent>"
	}
	return *s
}
