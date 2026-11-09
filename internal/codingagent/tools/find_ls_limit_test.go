package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

func limitFixture(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func limitDetails(t *testing.T, result agent.AgentToolResult) string {
	t.Helper()
	data, err := json.Marshal(result.Details)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Pi main 676af1d77 test/tools.test.ts (#7121), find and ls.
func TestFindLimitEdgeCases7121(t *testing.T) {
	t.Run("reports the result limit only when more results exist", func(t *testing.T) {
		dir := limitFixture(t, "a.txt", "b.txt", "c.txt")
		exact := runFind(t, dir, map[string]any{"pattern": "*.txt", "path": dir, "limit": 3})
		got := strings.Split(exact.Text(), "\n")
		slices.Sort(got)
		if !reflect.DeepEqual(got, []string{"a.txt", "b.txt", "c.txt"}) || exact.Details != nil {
			t.Fatalf("exact: %q %v", exact.Text(), exact.Details)
		}
		over := runFind(t, dir, map[string]any{"pattern": "*.txt", "path": dir, "limit": 2})
		parts := strings.Split(over.Text(), "\n\n")
		if len(strings.Split(parts[0], "\n")) != 2 || parts[1] != "[2 results limit reached. Use limit=4 for more, or refine pattern]" || limitDetails(t, over) != `{"resultLimitReached":2}` {
			t.Fatalf("over: %q %s", over.Text(), limitDetails(t, over))
		}
	})
	t.Run("treats limits below 1 as 1 and floors fractional limits", func(t *testing.T) {
		dir := limitFixture(t, "a.txt", "b.txt", "c.txt")
		for _, limit := range []float64{0, -5, 0.5, 1.9} {
			result := runFind(t, dir, map[string]any{"pattern": "*.txt", "path": dir, "limit": limit})
			parts := strings.Split(result.Text(), "\n\n")
			if len(parts) != 2 || len(strings.Split(parts[0], "\n")) != 1 || parts[1] != "[1 results limit reached. Use limit=2 for more, or refine pattern]" || limitDetails(t, result) != `{"resultLimitReached":1}` {
				t.Fatalf("limit %v: %q %s", limit, result.Text(), limitDetails(t, result))
			}
		}
	})
	t.Run("asks custom glob operations for one result past the limit", func(t *testing.T) {
		var requested []float64
		tool := func(results []string) *FindTool {
			return &FindTool{CWD: t.TempDir(), Operations: &FindOperations{
				Exists: func(string) (bool, error) { return true, nil },
				Glob: func(_, _ string, o FindGlobOptions) ([]string, error) {
					requested = append(requested, o.Limit)
					return results[:min(len(results), int(o.Limit))], nil
				},
			}}
		}
		exact, err := tool([]string{"a.txt", "b.txt"}).Execute(t.Context(), "", json.RawMessage(`{"pattern":"*.txt","limit":2}`), nil)
		if err != nil || exact.Text() != "a.txt\nb.txt" || exact.Details != nil {
			t.Fatalf("exact: %q %v %v", exact.Text(), exact.Details, err)
		}
		over, err := tool([]string{"a.txt", "b.txt", "c.txt"}).Execute(t.Context(), "", json.RawMessage(`{"pattern":"*.txt","limit":0}`), nil)
		if err != nil || over.Text() != "a.txt\n\n[1 results limit reached]" || limitDetails(t, over) != `{"resultLimitReached":1}` {
			t.Fatalf("over: %q %s %v", over.Text(), limitDetails(t, over), err)
		}
		if !reflect.DeepEqual(requested, []float64{3, 2}) {
			t.Fatalf("requested limits = %v", requested)
		}
	})
}

func TestLsLimitEdgeCases7121(t *testing.T) {
	t.Run("treats limits below 1 as 1 and floors fractional limits", func(t *testing.T) {
		dir := limitFixture(t, "a.txt", "b.txt", "c.txt")
		for _, limit := range []string{"0", "-5", "0.5", "1.9"} {
			text, _, details := lsRun(t, &LsTool{CWD: dir}, `{"path":`+jsonString(dir)+`,"limit":`+limit+`}`)
			data, _ := json.Marshal(details)
			if text != "a.txt\n\n[1 entries limit reached. Use limit=2 for more]" || string(data) != `{"entryLimitReached":1}` {
				t.Fatalf("limit %s: %q %s", limit, text, data)
			}
		}
	})
	t.Run("reports the entry limit only when more entries exist", func(t *testing.T) {
		dir := limitFixture(t, "a.txt", "b.txt", "c.txt")
		text, _, details := lsRun(t, &LsTool{CWD: dir}, `{"path":`+jsonString(dir)+`,"limit":3}`)
		if text != "a.txt\nb.txt\nc.txt" || details != nil {
			t.Fatalf("%q %v", text, details)
		}
	})
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }
