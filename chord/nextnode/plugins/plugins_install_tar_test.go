package plugins

// pi: packages/chord/src/node/plugins/install.ts

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Ports the npm-tarball case of packages/chord/test/node/plugins/install.test.ts, with the tar of Git for Windows in PATH: it reads the archive
// `C:\...` as the file `...` on a host `C` ("Cannot connect to C: resolve failed"), so an absolute archive fails there, and a `-C C:\...` destination is read relative to its working directory, so an absolute destination fails
// there (D616). The fake tar here refuses an absolute archive and an absolute destination, and extracts with the real one otherwise.
func TestInstallPluginNamesTheTarballToTarRelativeToThePackDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tar is a POSIX shell script; the Windows jobs run TestInstallPluginInstallsAnNpmPackageHereALocalTarballIntoRootName against Git for Windows' tar")
	}
	realTar, err := exec.LookPath("tar")
	if err != nil {
		t.Fatal(err)
	}
	source := tempRoot(t)
	writeFiles(t, source, map[string]string{
		"package.json": manifestText(t, map[string]any{"name": "@bob/clock", "version": "1.2.0"}),
		"worker.ts":    "export default { setup: () => {} };",
	})
	pack := exec.Command("npm", "pack", "--silent", "--pack-destination", source)
	pack.Dir = source
	out, err := pack.Output()
	if err != nil {
		t.Fatal(err)
	}
	packed := strings.TrimSpace(string(out))

	bin := t.TempDir()
	record := filepath.Join(bin, "archive.txt")
	script := "#!/bin/sh\n" +
		"printf '%s\\n%s\\n' \"$2\" \"$5\" > '" + record + "'\n" +
		"case $2 in /*) echo \"tar (child): Cannot connect to $2: resolve failed\" >&2; exit 2;; esac\n" +
		"case $5 in /*) echo \"tar: $5: Cannot open: No such file or directory\" >&2; exit 2;; esac\n" +
		"exec '" + realTar + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "tar"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	root := tempRoot(t)
	names, err := InstallPlugin(PluginSource{Npm: "@bob/clock", Version: "file:" + filepath.Join(source, packed)}, root, background, InstallPluginOptions{})
	if err != nil || !reflect.DeepEqual(names, []string{"@bob/clock"}) {
		t.Fatalf("names = %v, error = %v", names, err)
	}
	archive, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	recorded := strings.Split(strings.TrimSpace(string(archive)), "\n")
	if len(recorded) != 2 || recorded[0] != "./"+packed || recorded[1] != "../package" {
		t.Fatalf("tar got the archive and destination %q, want [./%s ../package]", recorded, packed)
	}
	if _, err := os.Stat(filepath.Join(root, "@bob", "clock", "package.json")); err != nil {
		t.Fatalf("the package was not extracted into the plugin directory: %v", err)
	}
}
