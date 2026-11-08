package runtime

import (
	"strings"
	"testing"
)

// Ports "keeps the tail within the bash tool's limits" (apps/p2/test/p2/shell.test.ts).
func TestShellTailKeepsTheTailWithinTheBashToolsLimits(t *testing.T) {
	var lines []string
	for i := range 2500 {
		lines = append(lines, "line "+jsNumber(float64(i)))
	}
	tail, truncated := ShellTailDefault(strings.Join(lines, "\n"))
	if !truncated || len(strings.Split(tail, "\n")) != 2000 || !strings.HasPrefix(tail, "line 500\n") {
		t.Fatalf("2500 lines: truncated=%v lines=%d head=%q", truncated, len(strings.Split(tail, "\n")), tail[:12])
	}
	long, truncated := ShellTailDefault(strings.Repeat("x", 60*1024))
	if !truncated || len(long) != 50*1024 {
		t.Fatalf("one long line: truncated=%v len=%d", truncated, len(long))
	}
	if short, truncated := ShellTailDefault("short"); short != "short" || truncated {
		t.Fatalf("short = %q, %v", short, truncated)
	}
}

func TestShellTailCutsOnACharacterBoundary(t *testing.T) {
	tail, truncated := ShellTail("ééé", 10, 3)
	if !truncated || tail != "é" {
		t.Fatalf("tail = %q, %v; want a whole é", tail, truncated)
	}
}

func TestShellContextText(t *testing.T) {
	str := func(s string) *string { return &s }
	num := func(n float64) *float64 { return &n }
	for _, c := range []struct {
		name string
		data ShellData
		want string
	}{
		{"output", ShellData{Command: "ls", Output: "a\nb", ExitCode: num(0)}, "Ran `ls`\n```\na\nb\n```"},
		{"no output", ShellData{Command: "true", ExitCode: num(0)}, "Ran `true`\n(no output)"},
		{"cancelled", ShellData{Command: "sleep 9", Cancelled: true, Error: str("ignored")}, "Ran `sleep 9`\n(no output)\n\n(command cancelled)"},
		{"error", ShellData{Command: "x", Error: str("failed to start")}, "Ran `x`\n(no output)\n\nfailed to start"},
		{"exit code", ShellData{Command: "false", ExitCode: num(1)}, "Ran `false`\n(no output)\n\nCommand exited with code 1"},
		{"truncated", ShellData{Command: "cat big", Output: "z", ExitCode: num(0), Truncated: true, FullOutputPath: str("/tmp/full")}, "Ran `cat big`\n```\nz\n```\n\n[Output truncated. Full output: /tmp/full]"},
	} {
		if got := ShellContextText(c.data); got != c.want {
			t.Errorf("%s = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsInterruptedError(t *testing.T) {
	if !IsInterruptedError("Tool bash was interrupted and may have partially run; not run again") {
		t.Error("interrupted error not recognised")
	}
	for _, not := range []any{"Tool  was interrupted and may have partially run", "tool bash was interrupted", "boom", nil, 3.0} {
		if IsInterruptedError(not) {
			t.Errorf("IsInterruptedError(%v) = true", not)
		}
	}
}

func TestSafeFileName(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd":   "passwd",
		`C:\dir\my file.TXT`: "my_file.TXT",
		"...hidden":          "hidden",
		"":                   "file",
		"/":                  "file",
		"ﬁle.txt":            "file.txt", // NFKC folds the ligature
	} {
		if got := SafeFileName(in); got != want {
			t.Errorf("SafeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SafeFileName("a" + strings.Repeat("b", 200)); got != strings.Repeat("b", 120) {
		t.Errorf("a long name keeps its last 120 units, got %q (%d)", got, len(got))
	}
}

func TestPreviewOf(t *testing.T) {
	for name, want := range map[string]FilePreview{
		"notes.MD":  {PreviewText, "text/plain; charset=utf-8"},
		"a.png":     {PreviewImage, "image/png"},
		"song.flac": {PreviewAudio, "audio/flac"},
		"clip.mov":  {PreviewVideo, "video/quicktime"},
		"png":       {PreviewImage, "image/png"}, // no dot: the whole name is the extension, as in upstream
	} {
		if got, ok := PreviewOf(name); !ok || got != want {
			t.Errorf("PreviewOf(%q) = %v, %v", name, got, ok)
		}
	}
	for _, name := range []string{"x.svg", "x.exe", "noext", "x."} {
		if _, ok := PreviewOf(name); ok {
			t.Errorf("PreviewOf(%q) previews", name)
		}
	}
}
