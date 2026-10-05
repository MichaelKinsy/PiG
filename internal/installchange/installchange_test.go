package installchange_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/installchange"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var (
	probeOnce sync.Once
	probePath string
	probeErr  error
)

// buildProbe compiles internal/installchange/testdata/probe once, so each test installs its own copy of a real executable.
func buildProbe(t *testing.T) string {
	t.Helper()
	probeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "pig-installchange-probe-")
		if err != nil {
			probeErr = err
			return
		}
		probePath = filepath.Join(dir, "probe"+exeSuffix())
		output, err := exec.Command("go", "build", "-o", probePath, "./testdata/probe").CombinedOutput()
		if err != nil {
			probeErr = errors.New(string(output))
		}
	})
	if probeErr != nil {
		t.Fatalf("build probe: %v", probeErr)
	}
	return probePath
}

func TestMain(m *testing.M) {
	code := m.Run()
	if probePath != "" {
		_ = os.RemoveAll(filepath.Dir(probePath))
	}
	os.Exit(code)
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func copyFile(t *testing.T, from, to string, extra []byte) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, append(data, extra...), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runningProbe is a started copy of the probe whose own file the test changes while it runs.
type runningProbe struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  *os.File
	reader *bufio.Reader
}

func startProbe(t *testing.T, path string, tracked ...string) *runningProbe {
	t.Helper()
	cmd := exec.Command(path, tracked...)
	in, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = in
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = in.Close()
	p := &runningProbe{t: t, cmd: cmd, stdin: out, reader: bufio.NewReader(stdout)}
	t.Cleanup(func() {
		_ = out.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if line := p.line(); line != "ready" {
		t.Fatalf("probe said %q, want ready", line)
	}
	return p
}

func (p *runningProbe) line() string {
	p.t.Helper()
	done := make(chan string, 1)
	go func() {
		line, _ := p.reader.ReadString('\n')
		done <- strings.TrimSpace(line)
	}()
	select {
	case line := <-done:
		return line
	case <-time.After(30 * time.Second):
		p.t.Fatal("probe did not answer")
		return ""
	}
}

// detect asks the running probe what Detect reports now.
func (p *runningProbe) detect() string {
	p.t.Helper()
	if _, err := p.stdin.WriteString("check\n"); err != nil {
		p.t.Fatal(err)
	}
	return p.line()
}

// moveAway removes a running executable. Windows refuses to delete or overwrite one that runs but lets a rename move it.
func moveAway(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// Ports packages/coding-agent/test/config.test.ts "detectInstallChange › reports a removed install instead of reading a package.json further up"
// (Pi 1.0.3, #10439): the install directory is gone and a same-named file further up must not stand in for it.
func TestDetectInstallChangeReportsARemovedInstallInsteadOfReadingAFileFurtherUp(t *testing.T) {
	tempDir := t.TempDir()
	installDir := filepath.Join(tempDir, "global", "hash")
	installed := filepath.Join(installDir, "probe"+exeSuffix())
	copyFile(t, buildProbe(t), installed, nil)
	// Pi writes a package.json with another version in tempDir. PiG's decoy is a different executable of the same name.
	copyFile(t, buildProbe(t), filepath.Join(tempDir, "probe"+exeSuffix()), []byte("decoy"))
	p := startProbe(t, installed)
	if got := p.detect(); got != "none" {
		t.Fatalf("an untouched install reported %q", got)
	}

	if runtime.GOOS == "windows" {
		moveAway(t, installed)
	} else if err := os.RemoveAll(installDir); err != nil {
		t.Fatal(err)
	}
	got := p.detect()
	if !strings.HasPrefix(got, string(installchange.BinaryRemoved)+" ") {
		t.Fatalf("a removed install reported %q, want %q", got, installchange.BinaryRemoved)
	}
}

func TestDetectInstallChangeReportsAReplacedExecutable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replace func(t *testing.T, path string)
	}{
		{"renamed over", func(t *testing.T, path string) {
			next := path + ".next"
			copyFile(t, buildProbe(t), next, []byte("newer"))
			moveAway(t, path)
			if err := os.Rename(next, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"removed and recreated", func(t *testing.T, path string) {
			moveAway(t, path)
			copyFile(t, buildProbe(t), path, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installed := filepath.Join(t.TempDir(), "probe"+exeSuffix())
			copyFile(t, buildProbe(t), installed, nil)
			p := startProbe(t, installed)
			if got := p.detect(); got != "none" {
				t.Fatalf("an untouched install reported %q", got)
			}
			tc.replace(t, installed)
			if got := p.detect(); !strings.HasPrefix(got, string(installchange.BinaryReplaced)+" ") {
				t.Fatalf("a replaced executable reported %q, want %q", got, installchange.BinaryReplaced)
			}
		})
	}
}

func TestDetectInstallChangeReportsPrunedCellFiles(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "probe"+exeSuffix())
	copyFile(t, buildProbe(t), installed, nil)
	cell := filepath.Join(dir, "cache", "cell-a", "cell")
	other := filepath.Join(dir, "cache", "cell-b", "cell")
	for _, path := range []string{cell, other} {
		copyFile(t, installed, path, nil)
	}
	p := startProbe(t, installed, cell, other)
	if got := p.detect(); got != "none" {
		t.Fatalf("intact cells reported %q", got)
	}
	if err := os.RemoveAll(filepath.Dir(other)); err != nil {
		t.Fatal(err)
	}
	if got, want := p.detect(), string(installchange.FilesPruned)+" "+other; got != want {
		t.Fatalf("a pruned cell reported %q, want %q", got, want)
	}
}

func TestTrackerRecordsDeviceInodeSizeAndTime(t *testing.T) {
	installed := filepath.Join(t.TempDir(), "probe"+exeSuffix())
	copyFile(t, buildProbe(t), installed, nil)
	tracker := installchange.NewTracker(installed)
	got, ok := tracker.Executable()
	if !ok {
		t.Fatal("the executable was not recorded")
	}
	want, err := os.Stat(installed)
	if err != nil {
		t.Fatal(err)
	}
	if got.Size != want.Size() || !got.ModTime.Equal(want.ModTime()) {
		t.Fatalf("recorded size/mtime = %d/%v, want %d/%v", got.Size, got.ModTime, want.Size(), want.ModTime())
	}
	if got.File == (installchange.FileID{}) {
		t.Fatal("the device and file index are empty")
	}
	if resolved, err := filepath.EvalSymlinks(installed); err != nil || got.Path != resolved {
		t.Fatalf("recorded path = %q, want the symlink-resolved %q (%v)", got.Path, resolved, err)
	}
}

func TestTrackerFollowsASymlinkedExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs a privilege on Windows")
	}
	dir := t.TempDir()
	first := filepath.Join(dir, "versions", "1", "probe")
	second := filepath.Join(dir, "versions", "2", "probe")
	copyFile(t, buildProbe(t), first, nil)
	copyFile(t, buildProbe(t), second, []byte("v2"))
	link := filepath.Join(dir, "bin", "probe")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, first, link)
	tracker := installchange.NewTracker(link)
	if change := tracker.Detect(); change != nil {
		t.Fatalf("an untouched symlinked install reported %+v", change)
	}
	// The old version stays on disk, but the path the user starts is another executable now.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, second, link)
	if change := tracker.Detect(); change == nil || change.Kind != installchange.BinaryReplaced {
		t.Fatalf("a repointed link reported %+v, want %q", change, installchange.BinaryReplaced)
	}
}

// A tracked path whose directory cannot be searched cannot say whether the file is gone, so it is no change.
func TestUnreadableTrackedFileIsNotAChange(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the test user cannot search")
	}
	dir := t.TempDir()
	installed := filepath.Join(dir, "probe"+exeSuffix())
	copyFile(t, buildProbe(t), installed, nil)
	tracker := installchange.NewTracker(installed)
	locked := filepath.Join(dir, "locked")
	copyFile(t, installed, filepath.Join(locked, "cell"), nil)
	tracker.TrackFile(filepath.Join(locked, "cell"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if change := tracker.Detect(); change != nil {
		t.Fatalf("an unsearchable cell directory reported %+v, want no change", change)
	}
}

func TestTrackedFileBelowARegularFileIsPruned(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "probe"+exeSuffix())
	copyFile(t, buildProbe(t), installed, nil)
	tracker := installchange.NewTracker(installed)
	// A tracked path whose parent is a regular file reports ENOTDIR, which only says the file is gone.
	file := filepath.Join(dir, "plain")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tracker.TrackFile(filepath.Join(file, "cell"))
	if change := tracker.Detect(); change == nil || change.Kind != installchange.FilesPruned {
		t.Fatalf("a tracked file below a regular file reported %+v, want %q", change, installchange.FilesPruned)
	}
}

func TestNilAndUnrecordedTrackersReportNothing(t *testing.T) {
	var none *installchange.Tracker
	none.TrackFile("x")
	if change := none.Detect(); change != nil {
		t.Fatalf("a nil tracker reported %+v", change)
	}
	if change := installchange.NewTracker(filepath.Join(t.TempDir(), "missing")).Detect(); change != nil {
		t.Fatalf("an executable absent at startup reported %+v", change)
	}
}

func TestDetectIsStableAndBounded(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "probe"+exeSuffix())
	copyFile(t, buildProbe(t), installed, nil)
	tracker := installchange.NewTracker(installed)
	for range 10000 {
		tracker.TrackFile(filepath.Join(dir, "cell", "same"))
	}
	if got := len(tracker.TrackedFiles()); got != 1 {
		t.Fatalf("one path tracked %d times is recorded %d times", 10000, got)
	}
}
