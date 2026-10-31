package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeExactIDSessions writes files session files of msgs messages each under dir, with ids 00000001-0000-4000-8000-<n>, and returns the last id.
func writeExactIDSessions(tb testing.TB, dir string, files, msgs int) string {
	tb.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		tb.Fatal(err)
	}
	var id string
	for i := range files {
		id = fmt.Sprintf("%08x-0000-4000-8000-%012x", 1, i)
		var sb strings.Builder
		fmt.Fprintf(&sb, `{"type":"session","version":3,"id":"%s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/work/proj0"}`+"\n", id)
		parent := "null"
		for m := range msgs {
			role := "user"
			if m%2 == 1 {
				role = "assistant"
			}
			fmt.Fprintf(&sb, `{"type":"message","id":"e%d","parentId":%s,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"%s","content":[{"type":"text","text":"%s"}],"timestamp":%d}}`+"\n", m, parent, role, strings.Repeat("lorem ipsum ", 40), 1767225600000+m)
			parent = fmt.Sprintf(`"e%d"`, m)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("2026-01-01T00-00-00-000Z_%s.jsonl", id)), []byte(sb.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return id
}

// BenchmarkResolveExactSessionID is --session <exact id> resolution over 20 sessions whose size grows.
func BenchmarkResolveExactSessionID(b *testing.B) {
	for _, msgs := range []int{10, 100, 1000, 10000} {
		dir := filepath.Join(b.TempDir(), "s")
		id := writeExactIDSessions(b, dir, 20, msgs)
		b.Run(fmt.Sprintf("msgs%d", msgs), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sel, err := resolveStartupSessionSelectionIn(Args{Session: id}, "/work/proj0", dir)
				if err != nil || sel.resumePath == "" {
					b.Fatal(err, sel)
				}
			}
		})
	}
}

// Pi main.ts resolveSessionPath tries the header-only findById first and lists sessions only for prefixes.
func TestResolveSessionArgumentRows(t *testing.T) {
	const cwd = "/work/proj0"
	dir := filepath.Join(t.TempDir(), "s")
	last := writeExactIDSessions(t, dir, 3, 2)
	paths := map[string]string{}
	for i := range 3 {
		id := fmt.Sprintf("%08x-0000-4000-8000-%012x", 1, i)
		paths[id] = filepath.Join(dir, "2026-01-01T00-00-00-000Z_"+id+".jsonl")
	}
	// A file that is not a session, and a poisoned one, share the directory.
	if err := os.WriteFile(filepath.Join(dir, "garbage.jsonl"), []byte("\x00\xff not json\n{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "poison.jsonl"), []byte(`{"type":"session","id":`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Distinct modification times fix the list order the ambiguous prefix takes its first match from.
	base := time.Now().Add(-time.Hour)
	i := 0
	for _, path := range paths {
		if err := os.Chtimes(path, base, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		i++
	}
	if err := os.Chtimes(paths[last], base, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, arg, want string
		notFound        bool
	}{
		{name: "exact id", arg: fmt.Sprintf("%08x-0000-4000-8000-%012x", 1, 1), want: paths[fmt.Sprintf("%08x-0000-4000-8000-%012x", 1, 1)]},
		{name: "unique prefix", arg: "00000001-0000-4000-8000-000000000002", want: paths[last]},
		{name: "ambiguous prefix takes the most recent", arg: "00000001", want: paths[last]},
		{name: "missing id", arg: "ffffffff", notFound: true},
		{name: "path argument", arg: "rel/none.jsonl", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := resolveStartupSessionSelectionIn(Args{Session: tc.arg}, cwd, dir)
			if tc.notFound {
				if err == nil || err.Error() != "No session found matching '"+tc.arg+"'" {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "path argument" {
				if sel.resumePath != "/work/proj0/rel/none.jsonl" {
					t.Fatalf("resumePath = %q", sel.resumePath)
				}
				return
			}
			if sel.resumePath != tc.want {
				t.Fatalf("resumePath = %q, want %q", sel.resumePath, tc.want)
			}
		})
	}
	t.Run("fork by exact id", func(t *testing.T) {
		id := fmt.Sprintf("%08x-0000-4000-8000-%012x", 1, 0)
		sel, err := resolveStartupSessionSelectionIn(Args{Fork: id}, cwd, dir)
		if err != nil || sel.forkPath == "" {
			t.Fatalf("fork = %+v, %v", sel, err)
		}
	})
}
