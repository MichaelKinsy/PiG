package codingagent

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func captureCouldNotReadStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	os.Stderr = old
	_ = w.Close()
	return <-done
}

// TestCouldNotReadWarningsMatchPi pins resource-loader.ts's two readFileSync warnings, probed on Pi 1.1.0 with stdout not a terminal:
// `pi -p hi --system-prompt <dir>` writes "Warning: Could not read system prompt file <dir>: Error: EISDIR: illegal operation on a
// directory, read", and an unreadable AGENTS.md writes "Warning: Could not read <path>: Error: EACCES: permission denied, open '<path>'".
func TestCouldNotReadWarningsMatchPi(t *testing.T) {
	t.Setenv("FORCE_COLOR", "0")
	dir := t.TempDir()
	got := captureCouldNotReadStderr(t, func() {
		if result := ResolvePromptInput(dir, "system prompt"); result != dir {
			t.Errorf("ResolvePromptInput(directory) = %q, want the input back", result)
		}
	})
	if want := "Warning: Could not read system prompt file " + dir + ": Error: EISDIR: illegal operation on a directory, read\n"; got != want {
		t.Errorf("directory prompt warning\n got %q\nwant %q", got, want)
	}

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("an unreadable file needs POSIX permissions and a non-root user")
	}
	project := t.TempDir()
	agents := filepath.Join(project, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("rules"), 0o000); err != nil {
		t.Fatal(err)
	}
	got = captureCouldNotReadStderr(t, func() {
		if file := loadContextFileFromDir(project); file != nil {
			t.Errorf("loadContextFileFromDir loaded an unreadable file: %+v", file)
		}
	})
	// resource-loader.ts:185-200 tries each candidate that existsSync finds and warns for every one it cannot read. On a
	// case-insensitive file system (macOS) AGENTS.MD names the same file as AGENTS.md, so Pi warns twice, under each spelling it asked
	// for (Pi 1.2.0 probed on darwin); on a case-sensitive one only AGENTS.md exists and Pi warns once.
	want := ""
	for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"} {
		path := filepath.Join(project, name)
		if _, err := os.Stat(path); err == nil {
			want += "Warning: Could not read " + path + ": Error: EACCES: permission denied, open '" + path + "'\n"
		}
	}
	if got != want {
		t.Errorf("unreadable context file warning\n got %q\nwant %q", got, want)
	}
}
