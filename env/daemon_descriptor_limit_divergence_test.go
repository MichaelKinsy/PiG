//go:build unix

package env

// pi: packages/env/daemon/src/main.rs

// D197: the descriptor limit of PiG's daemon is the hard limit, where Pi's daemon keeps the soft limit it inherited. Both
// daemons start with a soft limit of 64 open files and the same hard limit, and open directories (one descriptor each) until
// one refuses.

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/env/daemon"
)

// opensBeforeRefusal counts the opendir requests that succeed, up to attempts, before the first one fails, and returns that
// failure's code ("" when none fails).
func opensBeforeRefusal(t *testing.T, binary string, softLimit string, attempts int) (opened int, code string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "root")
	home := filepath.Join(base, "home")
	tmp := filepath.Join(base, "tmp")
	for _, dir := range []string{filepath.Join(root, "dir"), home, tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d := startWireDaemonWithSoftLimit(t, binary, root, daemonEnvironment(home, tmp), softLimit)
	for range attempts {
		frames := d.call(daemon.Object{"op": "opendir", "path": filepath.Join(root, "dir")}, nil)
		last := frames[len(frames)-1]
		if last.Kind == daemon.FrameError {
			failure, _ := last.JSON.(daemon.Object)
			code, _ = failure["code"].(string)
			return opened, code
		}
		opened++
	}
	return opened, ""
}

func TestDaemonDescriptorLimitIsTheHardLimitUnlikePisDaemon(t *testing.T) {
	const softLimit, attempts = 64, 300
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	if limit.Max < attempts+100 {
		t.Skipf("the hard limit of open files is %d: PiG's daemon could not open %d directories", limit.Max, attempts)
	}
	piOpened, piCode := opensBeforeRefusal(t, oracleDaemon(t), strconv.Itoa(softLimit), attempts)
	pigOpened, pigCode := opensBeforeRefusal(t, daemonBinary(t), strconv.Itoa(softLimit), attempts)
	// Pi's daemon never raises the soft limit it inherits, so the system refuses its descriptors below it.
	if piCode != "EMFILE" || piOpened >= softLimit {
		t.Errorf("Pi's daemon opened %d directories and then failed with %q; want fewer than the soft limit %d, then EMFILE", piOpened, piCode, softLimit)
	}
	// PiG's runtime raised the soft limit to the hard one at startup (D197), so the same limit lets it through.
	if pigCode != "" || pigOpened != attempts {
		t.Errorf("PiG's daemon opened %d directories and then failed with %q; want all %d", pigOpened, pigCode, attempts)
	}
}
