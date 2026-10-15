package docsdrift

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A fenced Go sample that is a complete file (it starts with `package`) is compiled against the real packages. The sample is placed in a throwaway directory of this module through the go command's overlay, so it can import the module's packages, internal ones included, without a file ever being written into the tree. A fragment is checked for syntax and imports only, by checkGo.
func TestGoProgramSamplesCompile(t *testing.T) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	overlay := map[string]string{}
	var dirs []string
	var origin []sample
	for _, s := range allSamples(t) {
		if (s.Lang != "go" && s.Lang != "golang") || s.Skip != "" || !strings.HasPrefix(strings.TrimSpace(s.Body), "package ") {
			continue
		}
		n := len(origin)
		src := filepath.Join(tmp, "sample"+strconv.Itoa(n)+".go")
		if err := os.WriteFile(src, []byte(s.Body), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := "zz_docs_sample_" + strconv.Itoa(n)
		overlay[filepath.Join(root, "test", "docs-drift", dir, "main.go")] = src
		dirs = append(dirs, "./test/docs-drift/"+dir)
		origin = append(origin, s)
	}
	if len(origin) < 4 {
		t.Fatalf("found only %d complete Go samples; the extractor is broken", len(origin))
	}
	data, err := json.Marshal(map[string]any{"Replace": overlay})
	if err != nil {
		t.Fatal(err)
	}
	overlayFile := filepath.Join(tmp, "overlay.json")
	if err := os.WriteFile(overlayFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
	for i, dir := range dirs {
		s := origin[i]
		cmd := exec.Command("go", "build", "-overlay", overlayFile, "-o", os.DevNull, dir)
		cmd.Dir = root
		out, err := runCmdTimeout(cmd, 5*time.Minute)
		if err != nil {
			t.Errorf("%s:%d: the Go sample does not compile: %v\n%s", s.Src, s.Line, err, strings.ReplaceAll(out, tmp, "<sample>"))
		}
	}
}

func runCmdTimeout(cmd *exec.Cmd, d time.Duration) (string, error) {
	var buf strings.Builder
	cmd.Stdout, cmd.Stderr = &buf, &buf
	if err := cmd.Start(); err != nil {
		return "", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return buf.String(), err
	case <-time.After(d):
		_ = cmd.Process.Kill()
		<-done
		return buf.String(), os.ErrDeadlineExceeded
	}
}
