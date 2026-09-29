package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func printFindOperations(t *testing.T, name string, result agent.AgentToolResult) {
	t.Helper()
	data, err := json.Marshal([]any{result.Text(), extension.ToolResultDetailsFor(result.Details)})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("FIND_OPS %s %s\n", name, data)
}

func TestFindCustomGlobRootPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6104-find-root-relativization.test.ts:76
	t.Run("relativizes custom glob results against a root search path", func(t *testing.T) {
		tool := &FindTool{CWD: "/", FdPath: filepath.Join(t.TempDir(), "must-not-run"), Operations: &FindOperations{
			Exists: func(string) (bool, error) { return true, nil },
			Glob: func(pattern, cwd string, opts FindGlobOptions) ([]string, error) {
				if pattern != "**" || cwd != mustResolvePath(t, "/", ".") || opts.Limit != 1000 || !slices.Equal(opts.Ignore, []string{"**/node_modules/**", "**/.git/**"}) {
					t.Fatalf("glob args = %q, %q, %+v", pattern, cwd, opts)
				}
				return []string{"/home/user/project/", "/home/user/project/file.txt"}, nil
			},
		}}
		result := runFileTool(t, tool, t.Context(), map[string]any{"pattern": "**"})
		if result.IsError || result.Text() != "home/user/project/\nhome/user/project/file.txt" {
			t.Fatalf("result = %+v", result)
		}
		printFindOperations(t, "root", result)
	})
}

func TestFindOperationsPreserveNumericLimitDetails(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/tools/find.ts:143-172 stores the requested number, including zero and fractions.
	for _, limit := range []float64{0, 1.5} {
		t.Run(jsNumber(limit), func(t *testing.T) {
			tool := &FindTool{CWD: t.TempDir(), Operations: &FindOperations{Exists: func(string) (bool, error) { return true, nil }, Glob: func(_ string, _ string, opts FindGlobOptions) ([]string, error) {
				if opts.Limit != limit {
					t.Fatal(opts.Limit)
				}
				return []string{"a", "b"}, nil
			}}}
			result := runFileTool(t, tool, t.Context(), map[string]any{"pattern": "*", "limit": limit})
			if result.IsError || result.Text() != "a\nb\n\n["+jsNumber(limit)+" results limit reached]" {
				t.Fatal(result)
			}
			want := `{"resultLimitReached":` + jsNumber(limit) + `}`
			data, err := json.Marshal(extension.ToolResultDetailsFor(result.Details))
			if err != nil || string(data) != want {
				t.Fatalf("details = %s, %v; want %s", data, err, want)
			}
			printFindOperations(t, "limit-"+jsNumber(limit), result)
		})
	}
}

func TestFindZeroLimitDetailsThroughFD(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/tools/find.ts:290-309 keeps a reached zero limit present in details.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	result := runFind(t, dir, map[string]any{"pattern": "*.txt", "limit": 0})
	if result.IsError || result.Text() != "a.txt\n\n[0 results limit reached. Use limit=0 for more, or refine pattern]" {
		t.Fatal(result)
	}
	data, err := json.Marshal(extension.ToolResultDetailsFor(result.Details))
	if err != nil || string(data) != `{"resultLimitReached":0}` {
		t.Fatalf("details = %s, %v", data, err)
	}
	printFindOperations(t, "fd-zero", result)
}

func TestFindOperationsCancellationAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, abortAt string
		exists        bool
		failure       string
		want          string
		calls         []string
	}{
		{"already aborted", "before", true, "", "Operation aborted", []string{}},
		{"abort after exists", "exists", true, "disk offline", "Operation aborted", []string{"exists"}},
		{"abort after glob", "glob", true, "glob failed", "Operation aborted", []string{"exists", "glob"}},
		{"missing path", "", false, "", "Path not found: ", []string{"exists"}},
		{"existence error", "error", true, "disk offline", "disk offline", []string{"exists"}},
		{"empty result", "", true, "", "No files found matching pattern", []string{"exists", "glob"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := []string{}
			tool := &FindTool{CWD: t.TempDir(), Operations: &FindOperations{
				Exists: func(string) (bool, error) {
					calls = append(calls, "exists")
					if tc.abortAt == "exists" {
						cancel()
					}
					if tc.failure != "" && tc.abortAt != "glob" {
						return false, errors.New(tc.failure)
					}
					return tc.exists, nil
				},
				Glob: func(string, string, FindGlobOptions) ([]string, error) {
					calls = append(calls, "glob")
					if tc.abortAt == "glob" {
						cancel()
					}
					if tc.failure != "" {
						return nil, errors.New(tc.failure)
					}
					return nil, nil
				},
			}}
			if tc.abortAt == "before" {
				cancel()
			}
			result := runFileTool(t, tool, ctx, map[string]any{"pattern": "*"})
			want := tc.want
			if !tc.exists {
				want += tool.CWD
			}
			if result.Text() != want || result.IsError != (tc.name != "empty result") || !reflect.DeepEqual(calls, tc.calls) {
				t.Fatalf("result=%+v calls=%q want=%q", result, calls, want)
			}
		})
	}
}
