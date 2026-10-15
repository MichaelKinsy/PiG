package mcpext_test

// pi: packages/coding-agent/src/extensions/mcp/log.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

const logLimit = 5 * 1024 * 1024

// upstream mcp/log.ts McpServerLog.write: a log past MAX_LOG_BYTES is renamed to `mcp.log.1` before the next append, so
// the new file starts with the new message.
func TestMcpServerLogRotatesAFileThatGrewPastTheLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "mcp.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	old := strings.Repeat("x", logLimit+1)
	if err := os.WriteFile(path, []byte(old), 0o666); err != nil {
		t.Fatal(err)
	}
	mcpext.NewMcpServerLog(path).Write("s", json.RawMessage(`{"data":"fresh"}`))
	rotated, err := os.ReadFile(path + ".1")
	if err != nil || string(rotated) != old {
		t.Fatalf("mcp.log.1: %d bytes, %v; want the %d bytes of the old log", len(rotated), err, len(old))
	}
	current, err := os.ReadFile(path)
	if err != nil || !strings.HasSuffix(string(current), " [s] info fresh\n") || strings.Count(string(current), "\n") != 1 {
		t.Fatalf("mcp.log = %q, %v; want only the new message", current, err)
	}
}

// A file of exactly MAX_LOG_BYTES is not past the limit; one append crosses it and the next write rotates.
func TestMcpServerLogRotatesOnTheWriteAfterTheLimitIsCrossed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", logLimit)), 0o666); err != nil {
		t.Fatal(err)
	}
	log := mcpext.NewMcpServerLog(path)
	log.Write("s", json.RawMessage(`{"data":"one"}`))
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("rotated at exactly the limit")
	}
	log.Write("s", json.RawMessage(`{"data":"two"}`))
	rotated, err := os.ReadFile(path + ".1")
	if err != nil || !strings.HasSuffix(string(rotated), " [s] info one\n") || len(rotated) < logLimit {
		t.Fatalf("mcp.log.1: %d bytes, %v", len(rotated), err)
	}
	current, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(current), " [s] info two\n") || strings.Count(string(current), "\n") != 1 {
		t.Fatalf("mcp.log = %q", current)
	}
}

// Another process may have rotated the file already: the tracked size is past the limit but the file is not, so the
// log appends without renaming it again.
func TestMcpServerLogDoesNotRotateAFileAnotherProcessAlreadyRotated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", logLimit)), 0o666); err != nil {
		t.Fatal(err)
	}
	log := mcpext.NewMcpServerLog(path)
	log.Write("s", json.RawMessage(`{"data":"one"}`))
	if err := os.WriteFile(path, []byte("rotated by another process\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	log.Write("s", json.RawMessage(`{"data":"two"}`))
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Fatal("renamed a file that was no longer past the limit")
	}
	current, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(current), "rotated by another process\n") || !strings.HasSuffix(string(current), " [s] info two\n") {
		t.Fatalf("mcp.log = %q", current)
	}
}

// log.ts write compares both sizes with `>`: the tracked size first, then the file's current size. A tracked size of
// exactly MAX_LOG_BYTES does not rotate, even when another process has grown the file past it; a file of exactly
// MAX_LOG_BYTES is not renamed, even when the tracked size is past it.
func TestMcpServerLogComparesTheTrackedAndTheCurrentSizeStrictlyWithTheLimit(t *testing.T) {
	line := len(mcpext.FormatMcpLogMessage("s", json.RawMessage(`{"data":"one"}`), time.Now()))
	setup := func(t *testing.T, initial int) (*mcpext.McpServerLog, string) {
		path := filepath.Join(t.TempDir(), "mcp.log")
		if err := os.WriteFile(path, []byte(strings.Repeat("x", initial)), 0o666); err != nil {
			t.Fatal(err)
		}
		log := mcpext.NewMcpServerLog(path)
		log.Write("s", json.RawMessage(`{"data":"one"}`))
		return log, path
	}
	t.Run("tracked size at the limit", func(t *testing.T) {
		log, path := setup(t, logLimit-line)
		if err := os.WriteFile(path, []byte(strings.Repeat("y", logLimit+1)), 0o666); err != nil {
			t.Fatal(err)
		}
		log.Write("s", json.RawMessage(`{"data":"two"}`))
		if _, err := os.Stat(path + ".1"); err == nil {
			t.Fatal("rotated with a tracked size of exactly the limit")
		}
	})
	t.Run("file at the limit", func(t *testing.T) {
		log, path := setup(t, logLimit-line+1)
		if err := os.WriteFile(path, []byte(strings.Repeat("y", logLimit)), 0o666); err != nil {
			t.Fatal(err)
		}
		log.Write("s", json.RawMessage(`{"data":"two"}`))
		if _, err := os.Stat(path + ".1"); err == nil {
			t.Fatal("renamed a file of exactly the limit")
		}
	})
}
