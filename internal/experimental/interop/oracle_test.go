// Package interop proves protocol-version-8 wire compatibility between the Go remote Session stack and the pinned Pi
// Node packages (packages/protocol, packages/client, packages/server): each Go component is driven against the upstream
// TypeScript sources, in both directions.
package interop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// root is the repository root; go test runs in this package directory.
func root(t testing.TB) string {
	t.Helper()
	path, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func fileURL(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "file://" + path
}

// nodeScript starts a pinned-upstream Node script (testdata/<name>) with the workspace loader.
func nodeScript(ctx context.Context, t testing.TB, name string, args ...string) *exec.Cmd {
	t.Helper()
	top := root(t)
	arguments := append([]string{"--import", fileURL(filepath.Join(top, "internal/experimental/interop/testdata/loader.mjs")), filepath.Join(top, "internal/experimental/interop/testdata", name)}, args...)
	cmd := exec.CommandContext(ctx, "node", arguments...)
	cmd.Env = append(os.Environ(), "PIG_TEST_ROOT="+top, "HOME="+t.TempDir())
	return cmd
}

// oracleLines runs a line-oriented oracle: one JSON request per input line, one JSON reply per output line.
func oracleLines[T any](t testing.TB, script string, requests []any) []T {
	t.Helper()
	var input bytes.Buffer
	encoder := json.NewEncoder(&input)
	encoder.SetEscapeHTML(false)
	for _, request := range requests {
		if err := encoder.Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	cmd := nodeScript(t.Context(), t, script)
	cmd.Stdin = &input
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v\n%s", script, err, stderr.String())
	}
	var replies []T
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(nil, 1<<26)
	for scanner.Scan() {
		var reply T
		if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
			t.Fatalf("%s reply %q: %v", script, scanner.Text(), err)
		}
		replies = append(replies, reply)
	}
	if len(replies) != len(requests) {
		t.Fatalf("%s returned %d replies for %d requests\n%s", script, len(replies), len(requests), stderr.String())
	}
	return replies
}

// mismatches collects differential failures so one run reports every divergence, as the lane's mass-port rule requires.
type mismatches struct {
	mu    sync.Mutex
	lines []string
}

func (m *mismatches) add(format string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func (m *mismatches) report(t testing.TB, total int) {
	t.Helper()
	if len(m.lines) == 0 {
		return
	}
	shown := m.lines
	if len(shown) > 8 {
		shown = shown[:8]
	}
	t.Fatalf("%d of %d cases differ from the pinned Node packages:\n%s", len(m.lines), total, strings.Join(shown, "\n"))
}
