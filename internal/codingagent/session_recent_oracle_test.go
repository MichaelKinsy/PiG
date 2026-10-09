package codingagent

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type recentFile struct {
	Name    string
	Content string
	MTime   int64 // seconds; the sub-second part is set by Nano
	Nano    int64
	Dir     bool
}

type recentCase struct {
	Files []recentFile
	Cwd   string
}

func recentHeader(id, cwd string) string {
	return `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-01-0` + id[len(id)-1:] + `T00:00:00.000Z","cwd":"` + cwd + `"}` + "\n"
}

// TestFindMostRecentSessionMatchesPi compares findMostRecentSession of Pi 1.0.4 with the Pig discovery over session directories: ordering by mtime, ties, the header scan (blank and garbage leading lines, a header with no id, an ill-typed cwd), cwd filters, non-sessions and a directory that ends in .jsonl.
func TestFindMostRecentSessionMatchesPi(t *testing.T) {
	work := t.TempDir()
	cwd := filepath.Join(work, "proj")
	other := filepath.Join(work, "other")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	s1, s2, s3 := recentHeader("a1", cwd), recentHeader("a2", cwd), recentHeader("a3", cwd)
	o1 := recentHeader("o1", other)
	cases := []recentCase{
		{Files: []recentFile{{"a.jsonl", s1, 100, 0, false}, {"b.jsonl", s2, 300, 0, false}, {"c.jsonl", s3, 200, 0, false}}},
		{Files: []recentFile{{"a.jsonl", s1, 100, 0, false}, {"b.jsonl", s2, 100, 0, false}}},
		{Files: []recentFile{{"b.jsonl", s1, 100, 0, false}, {"a.jsonl", s3, 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", s3, 100, 500000, false}, {"b.jsonl", s1, 100, 100000, false}}},
		{Files: []recentFile{{"a.jsonl", s1, 100, 123400, false}, {"b.jsonl", s2, 100, 123900, false}}},
		{Files: []recentFile{{"a.jsonl", s1, 100, 0, false}, {"b.jsonl", o1, 200, 0, false}}, Cwd: cwd},
		{Files: []recentFile{{"a.jsonl", s1, 100, 0, false}, {"b.jsonl", o1, 200, 0, false}}},
		{Files: []recentFile{{"a.jsonl", o1, 100, 0, false}}, Cwd: cwd},
		{Files: []recentFile{{"a.jsonl", s1, 100, 0, false}}, Cwd: cwd + "/"},
		{Files: []recentFile{{"a.jsonl", s1, 100, 0, false}}, Cwd: cwd + "/../proj"},
		{Files: []recentFile{{"a.jsonl", recentHeader("a1", cwd+"/"), 100, 0, false}}, Cwd: cwd},
		{Files: []recentFile{{"a.jsonl", recentHeader("a1", cwd+"/../proj"), 100, 0, false}}, Cwd: cwd},
		{Files: []recentFile{{"a.jsonl", recentHeader("a1", ""), 100, 0, false}}, Cwd: cwd},
		{Files: []recentFile{{"a.jsonl", recentHeader("a1", ""), 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", `{"type":"session","version":3,"id":"x","timestamp":"2026-01-01T00:00:00.000Z","cwd":5}` + "\n", 100, 0, false}}, Cwd: cwd},
		{Files: []recentFile{{"a.jsonl", `{"type":"session","version":3,"id":"x","timestamp":"2026-01-01T00:00:00.000Z"}` + "\n", 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", `{"type":"session","version":3,"id":"x","timestamp":"2026-01-01T00:00:00.000Z"}` + "\n", 100, 0, false}}, Cwd: cwd},
		{Files: []recentFile{{"a.jsonl", "\n\n" + s1, 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", "garbage\n" + s1, 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", "null\n" + s1, 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", `{"type":"message"}` + "\n" + s1, 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", `{"type":"session","id":7}` + "\n" + s1, 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", `{"type":"session"}` + "\n" + s1, 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", s1[:len(s1)-1], 100, 0, false}}},
		{Files: []recentFile{{"a.jsonl", "", 100, 0, false}, {"b.jsonl", s1, 50, 0, false}}},
		{Files: []recentFile{{"a.jsonl", "not a session\n", 300, 0, false}, {"b.jsonl", s1, 50, 0, false}}},
		{Files: []recentFile{{"a.txt", s1, 300, 0, false}, {"b.jsonl", s2, 50, 0, false}}},
		{Files: []recentFile{{"a.jsonl", s1, 50, 0, true}, {"b.jsonl", s2, 20, 0, false}}},
		{Files: []recentFile{{"a.JSONL", s1, 50, 0, false}}},
		{Files: []recentFile{{"a.jsonl.bak", s1, 50, 0, false}}},
		{Files: []recentFile{{"é.jsonl", s1, 50, 0, false}, {"z.jsonl", s2, 50, 0, false}}},
		{Files: []recentFile{{"B.jsonl", s1, 50, 0, false}, {"a.jsonl", s2, 50, 0, false}}},
		{Files: []recentFile{{"a.jsonl", "\ufeff" + s1, 50, 0, false}}},
		{Files: []recentFile{{"a.jsonl", s1 + s2, 50, 0, false}}},
		{},
	}
	dirs := make([]string, len(cases))
	oracleDirs := make([]string, len(cases))
	for i, c := range cases {
		for k, base := range []*[]string{(&dirs), (&oracleDirs)} {
			dir := filepath.Join(work, []string{"pig", "pi"}[k], strconv.Itoa(i))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, f := range c.Files {
				p := filepath.Join(dir, f.Name)
				if f.Dir {
					if err := os.Mkdir(p, 0o755); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(p, []byte(f.Content), 0o644); err != nil {
					t.Fatal(err)
				}
				when := time.Unix(f.MTime, f.Nano)
				if err := os.Chtimes(p, when, when); err != nil {
					t.Fatal(err)
				}
			}
			(*base)[i] = dir
		}
	}
	type query struct {
		Dir string `json:"dir"`
		Cwd string `json:"cwd"`
	}
	queries := make([]query, len(cases))
	for i, c := range cases {
		queries[i] = query{oracleDirs[i], c.Cwd}
	}
	queries = append(queries, query{filepath.Join(work, "missing"), ""})
	var want []*string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
emit(input.map((q) => mod.findMostRecentSession(q.dir, q.cwd || undefined)));`, queries, &want)
	check := func(i int, got string, dir string) {
		wantPath := ""
		if want[i] != nil {
			wantPath = filepath.Join(dirs[i], filepath.Base(*want[i]))
		}
		if got != wantPath {
			t.Errorf("case %d (%+v): Pig %q, Pi %q", i, cases[i], filepath.Base(got), filepath.Base(wantPath))
		}
	}
	for i, c := range cases {
		sm := NewSessionManagerWithDir(cwd, dirs[i])
		check(i, sm.findMostRecent(c.Cwd != ""), dirs[i])
	}
	if got := NewSessionManagerWithDir(cwd, filepath.Join(work, "missing")).findMostRecent(false); got != "" || want[len(cases)] != nil {
		t.Errorf("missing dir: Pig %q, Pi %v", got, want[len(cases)])
	}
}
