//go:build !windows

package codingagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const (
	stateFileSiteEnv = "CODINGAGENT_TEST_STATE_FILE_SITE"
	stateFileDirEnv  = "CODINGAGENT_TEST_STATE_FILE_DIR"
)

// stateFileWriters are the first writes of the state files Pi writes with writeFileSync(path, data, "utf-8"), which gives the new file mode 0666 with the
// umask taken out; each returns the file it wrote.
var stateFileWriters = map[string]func(dir string) (string, error){
	// settings-manager.ts:245
	"settings.json": func(dir string) (string, error) {
		path := filepath.Join(dir, "settings.json")
		return path, saveSettingsPatch(path, Settings{}, Settings{Theme: "dark"})
	},
	// trust-manager.ts:142
	"trust.json": func(dir string) (string, error) {
		store := NewProjectTrustStore(dir)
		return store.trustPath, store.write(map[string]*bool{"/work": nil})
	},
	// migrations.ts:168: the only write of keybindings.json in Pi
	"keybindings.json": func(dir string) (string, error) {
		path := filepath.Join(dir, "keybindings.json")
		return path, NewKeybindingsManager(dir).Save(path)
	},
}

// TestStateFileWriterHelper is the child of TestStateFilesGetPiModeUnderUmask: it runs in a process whose umask the parent chose, and does nothing when
// run on its own.
func TestStateFileWriterHelper(t *testing.T) {
	site := os.Getenv(stateFileSiteEnv)
	if site == "" {
		return
	}
	if _, err := stateFileWriters[site](os.Getenv(stateFileDirEnv)); err != nil {
		t.Fatal(err)
	}
}

// Pi writes settings.json, trust.json and keybindings.json without a mode, so a new file is 0666 with the umask taken out: 0664 under umask 002, 0644 under
// 022, 0600 under 077.
func TestStateFilesGetPiModeUnderUmask(t *testing.T) {
	for _, site := range []string{"settings.json", "trust.json", "keybindings.json"} {
		for _, umask := range []os.FileMode{0o002, 0o022, 0o077} {
			t.Run(fmt.Sprintf("%s umask %04o", site, umask), func(t *testing.T) {
				dir := t.TempDir()
				child := exec.Command("sh", "-c", fmt.Sprintf(`umask %04o; exec "$0" -test.run='^TestStateFileWriterHelper$' -test.count=1`, umask), os.Args[0])
				child.Env = append(os.Environ(), stateFileSiteEnv+"="+site, stateFileDirEnv+"="+dir)
				if out, err := child.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				info, err := os.Stat(filepath.Join(dir, site))
				if err != nil {
					t.Fatal(err)
				}
				if want := 0o666 &^ umask; info.Mode().Perm() != want {
					t.Fatalf("mode of a new %s = %04o, want %04o (0666 with umask %04o taken out, as Pi's writeFileSync gives it)", site, info.Mode().Perm(), want, umask)
				}
			})
		}
	}
}
