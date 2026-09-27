// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
package ciimages

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// These tests run docs/site/public/install.sh (served at
// https://pi-in-go.dev/install.sh) offline: a fake curl on PATH serves
// fixture files for the release URLs, so every download, checksum, and
// install path runs for real against a temporary HOME. They were the
// hosting Worker's install-sh.test.ts before the site moved out of this
// repository; the installer stays public so users can audit it.

const (
	installVersion  = "0.2.0"
	installDownload = "https://github.com/MichaelKinsy/PiG/releases/download"
)

// fakeCurl answers `curl ... [-o FILE] URL` from $FIXTURES/<url without
// scheme>, exiting 22 (curl's HTTP error status under -f) when no fixture
// exists.
const fakeCurl = `#!/bin/sh
out=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out=$2; shift 2 ;;
    --proto | --retry) shift 2 ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
echo "$url" >> "$FIXTURES/.requests"
file="$FIXTURES/${url#https://}"
[ -f "$file" ] || exit 22
if [ -n "$out" ]; then cp "$file" "$out"; else cat "$file"; fi
`

type installFixture struct {
	root, home, installDir, release, archive, archiveSHA string
}

func installArchiveName() (name, archive string) {
	goos, arch := "linux", "amd64"
	if runtime.GOOS == "darwin" {
		goos = "darwin"
	}
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	name = "pig-" + installVersion + "-" + goos + "-" + arch
	return name, name + ".tar.gz"
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

// releaseArchive builds <name>/pig, a script printing the release version.
func releaseArchive(t *testing.T, name string) []byte {
	t.Helper()
	pig := []byte("#!/bin/sh\necho \"" + installVersion + "+" + coding.UpstreamVersion + "\"\n")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, header := range []*tar.Header{
		{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: name + "/pig", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(pig))},
	} {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tw.Write(pig); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newInstallFixture(t *testing.T) installFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh installs Linux and macOS releases; TestInstallShRefusesWindows covers Windows")
	}
	root := t.TempDir()
	name, archive := installArchiveName()
	fixtures := filepath.Join(root, "fixtures")
	release := filepath.Join(fixtures, strings.TrimPrefix(installDownload, "https://"), "v"+installVersion)
	data := releaseArchive(t, name)
	sum := sha256.Sum256(data)
	f := installFixture{
		root:       root,
		home:       filepath.Join(root, "home"),
		installDir: filepath.Join(root, "home", "bin"),
		release:    release,
		archive:    archive,
		archiveSHA: hex.EncodeToString(sum[:]),
	}
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "fakebin", "curl"), []byte(fakeCurl), 0o755)
	writeFile(t, filepath.Join(release, archive), data, 0o644)
	writeFile(t, filepath.Join(fixtures, "pi-in-go.dev", "api", "latest-version"), []byte(`{"version":"`+installVersion+`"}`), 0o644)
	f.writeSums(t, strings.Repeat("0", 64)+"  ./evidence/sbom.spdx.json", f.archiveSHA+"  ./"+archive)
	return f
}

func (f installFixture) writeSums(t *testing.T, lines ...string) {
	t.Helper()
	writeFile(t, filepath.Join(f.release, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func (f installFixture) removeAPI(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(f.root, "fixtures", "pi-in-go.dev")); err != nil {
		t.Fatal(err)
	}
}

type installResult struct {
	status         int
	stdout, stderr string
}

// run executes install.sh with only the fixture environment, as a user's
// `curl ... | sh` would, never the test process's own.
func (f installFixture) run(t *testing.T, extraEnv ...string) installResult {
	t.Helper()
	cmd := exec.Command(testenv.Sh(t), filepath.Join(repoRoot(t), "docs", "site", "public", "install.sh"))
	cmd.Env = append([]string{
		"PATH=" + filepath.Join(f.root, "fakebin") + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"HOME=" + f.home,
		"FIXTURES=" + filepath.Join(f.root, "fixtures"),
		"PIG_INSTALL_DIR=" + f.installDir,
	}, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	status := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		status = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run install.sh: %v", err)
	}
	return installResult{status: status, stdout: stdout.String(), stderr: stderr.String()}
}

func (f installFixture) assertNothingInstalled(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(f.installDir); !os.IsNotExist(err) {
		t.Fatalf("install directory exists after a refused install: %v", err)
	}
}

func assertFailure(t *testing.T, got installResult, want string) {
	t.Helper()
	if got.status != 1 {
		t.Fatalf("status %d, want 1\nstdout:\n%s\nstderr:\n%s", got.status, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, want) {
		t.Fatalf("stderr does not report %q:\n%s", want, got.stderr)
	}
}

func TestInstallShInstallsTheLatestReleaseAfterVerifyingItsSHA256(t *testing.T) {
	f := newInstallFixture(t)
	got := f.run(t)
	if got.status != 0 {
		t.Fatalf("status %d:\n%s", got.status, got.stderr)
	}
	if !strings.Contains(got.stdout, "Verified SHA-256 "+f.archiveSHA) {
		t.Fatalf("stdout does not report the verified digest:\n%s", got.stdout)
	}
	want := installVersion + "+" + coding.UpstreamVersion
	if !regexp.MustCompile(`Installed ` + regexp.QuoteMeta(want) + ` to `).MatchString(got.stdout) {
		t.Fatalf("stdout does not report the installed version %s:\n%s", want, got.stdout)
	}
	out, err := exec.Command(filepath.Join(f.installDir, "pig"), "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != want {
		t.Fatalf("installed pig --version = %q, want %q", out, want)
	}
}

func TestInstallShInstallsPigVersionWithoutAskingTheAPI(t *testing.T) {
	f := newInstallFixture(t)
	f.removeAPI(t)
	got := f.run(t, "PIG_VERSION=v"+installVersion)
	if got.status != 0 {
		t.Fatalf("status %d:\n%s", got.status, got.stderr)
	}
	requests, err := os.ReadFile(filepath.Join(f.root, "fixtures", ".requests"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(requests), "latest-version") {
		t.Fatalf("install.sh asked the API despite PIG_VERSION:\n%s", requests)
	}
}

func TestInstallShFailsClosedWhenNoReleaseIsPublished(t *testing.T) {
	f := newInstallFixture(t)
	f.removeAPI(t)
	assertFailure(t, f.run(t), "no PiG release is published yet")
	f.assertNothingInstalled(t)
}

func TestInstallShRefusesAnArchiveWhoseChecksumDoesNotMatch(t *testing.T) {
	f := newInstallFixture(t)
	f.writeSums(t, strings.Repeat("a", 64)+"  ./"+f.archive)
	assertFailure(t, f.run(t), "checksum mismatch")
	f.assertNothingInstalled(t)
}

func TestInstallShRefusesAReleaseWithoutOrWithAmbiguousSHA256SUMS(t *testing.T) {
	missing := newInstallFixture(t)
	if err := os.Remove(filepath.Join(missing.release, "SHA256SUMS")); err != nil {
		t.Fatal(err)
	}
	assertFailure(t, missing.run(t), "has no SHA256SUMS")
	missing.assertNothingInstalled(t)

	doubled := newInstallFixture(t)
	doubled.writeSums(t, doubled.archiveSHA+"  ./"+doubled.archive, doubled.archiveSHA+"  "+doubled.archive)
	assertFailure(t, doubled.run(t), "no single valid entry")
}

func TestInstallShRejectsAVersionThatIsNotSemVer(t *testing.T) {
	f := newInstallFixture(t)
	assertFailure(t, f.run(t, "PIG_VERSION=latest;rm"), "not a release version")
}

func TestInstallShRecordsAnOwnerOnlyReceiptForPigUpdate(t *testing.T) {
	for name, env := range map[string][]string{
		"default":         nil,
		"PIG_HOME":        {"PIG_HOME=CUSTOM"},
		"XDG_CONFIG_HOME": {"XDG_CONFIG_HOME=CUSTOM"},
		"PIG_UPDATE_URL":  {"PIG_UPDATE_URL=https://updates.example/pig/update.json"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newInstallFixture(t)
			custom := filepath.Join(f.root, "custom-config")
			var extra []string
			for _, e := range env {
				extra = append(extra, strings.Replace(e, "CUSTOM", custom, 1))
			}
			got := f.run(t, extra...)
			if got.status != 0 {
				t.Fatalf("status %d:\n%s", got.status, got.stderr)
			}
			receiptPath := filepath.Join(f.home, ".pig", "install-receipt")
			switch name {
			case "PIG_HOME":
				receiptPath = filepath.Join(custom, "install-receipt")
			case "XDG_CONFIG_HOME":
				receiptPath = filepath.Join(custom, "pig", "install-receipt")
			}
			info, err := os.Stat(receiptPath)
			if err != nil {
				t.Fatalf("no install receipt: %v\n%s", err, got.stdout)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("receipt mode = %o, want 600", info.Mode().Perm())
			}
			exe, err := filepath.EvalSymlinks(filepath.Join(f.installDir, "pig"))
			if err != nil {
				t.Fatal(err)
			}
			installed, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(installed)
			source := "https://github.com/MichaelKinsy/PiG/releases/latest/download/update.json"
			if name == "PIG_UPDATE_URL" {
				source = "https://updates.example/pig/update.json"
			}
			want := "kind=standalone\nexecutable=" + exe + "\npig-version=" + installVersion +
				"\nsha256=" + hex.EncodeToString(sum[:]) + "\nupdate-source=" + source + "\n"
			data, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != want {
				t.Fatalf("receipt =\n%s\nwant\n%s", data, want)
			}
		})
	}
}
