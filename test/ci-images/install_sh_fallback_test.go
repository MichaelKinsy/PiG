// SPDX-License-Identifier: MIT
package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const installLatest = "https://github.com/MichaelKinsy/PiG/releases/latest"

// GNU wget reports the Location header but exits nonzero when --max-redirect=0 stops a redirect.
const fakeWget = `#!/bin/sh
out=""
url=""
redirect=""
headers=""
while [ $# -gt 0 ]; do
  case "$1" in
    -O) out=$2; shift 2 ;;
    --max-redirect=0) redirect=1; shift ;;
    -S) headers=1; shift ;;
    -*) shift ;;
    *) url=$1; shift ;;
  esac
done
echo "$url" >> "$FIXTURES/.requests"
file="$FIXTURES/${url#https://}"
[ -f "$file" ] || exit 8
if [ -n "$redirect" ]; then
  [ "$headers" = 1 ] && [ "$out" = /dev/null ] || exit 2
  printf '  HTTP/1.1 302 Found\r\n  Location: %s\r\n' "$(cat "$file")" >&2
  printf 'Location: %s [following]\n' "$(cat "$file")" >&2
  echo '0 redirections exceeded.' >&2
  exit 8
fi
if [ "$out" = - ]; then cat "$file"; else cp "$file" "$out"; fi
`

func (f installFixture) downloaderEnv(t *testing.T, downloader string) []string {
	t.Helper()
	if downloader == "curl" {
		return nil
	}
	// Exclude the real curl so pick_downloader exercises its wget branch without network access.
	bin := filepath.Join(f.root, "wgetbin")
	writeFile(t, filepath.Join(bin, "wget"), []byte(fakeWget), 0o755)
	for _, name := range []string{"uname", "tar", "mktemp", "cat", "cp", "sed", "tr", "tail", "grep", "awk", "mkdir", "rm", "chmod", "mv", "gzip"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		testenv.RequireSymlink(t, path, filepath.Join(bin, name))
	}
	for _, name := range []string{"sha256sum", "shasum", "openssl"} {
		if path, err := exec.LookPath(name); err == nil {
			testenv.RequireSymlink(t, path, filepath.Join(bin, name))
		}
	}
	return []string{"PATH=" + bin}
}

func (f installFixture) writeRedirect(t *testing.T, url, location string) {
	t.Helper()
	writeFile(t, filepath.Join(f.root, "fixtures", strings.TrimPrefix(url, "https://")), []byte(location), 0o644)
}

func (f installFixture) assertRequests(t *testing.T, urls ...string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(f.root, "fixtures", ".requests"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join(urls, "\n") + "\n"
	if string(got) != want {
		t.Fatalf("requests =\n%s\nwant\n%s", got, want)
	}
}

// The site API can fail independently of release publication. Resolve only the tag from the redirect, then use the same verified download and receipt path.
func TestInstallShFallsBackToLatestRedirect(t *testing.T) {
	for _, downloader := range []string{"curl", "wget"} {
		for _, api := range []string{"404", `{}`, `{"version":""}`} {
			t.Run(downloader+"/"+api, func(t *testing.T) {
				f := newInstallFixture(t)
				f.removeAPI(t)
				if api != "404" {
					writeFile(t, filepath.Join(f.root, "fixtures", "pi-in-go.dev", "api", "latest-version"), []byte(api), 0o644)
				}
				f.writeRedirect(t, installLatest, "https://github.com/MichaelKinsy/PiG/releases/tag/v"+installVersion)
				got := f.run(t, f.downloaderEnv(t, downloader)...)
				if got.status != 0 || !strings.Contains(got.stdout, "Verified SHA-256 "+f.archiveSHA) || !strings.Contains(got.stdout, "Installed "+installVersion+"+") {
					t.Fatalf("fallback install = %+v", got)
				}
				if !strings.Contains(got.stderr, "using the latest on GitHub (v"+installVersion+")") {
					t.Fatalf("missing fallback diagnostic: %s", got.stderr)
				}
				if _, err := os.Stat(filepath.Join(f.home, ".pig", "install-receipt")); err != nil {
					t.Fatal(err)
				}
				f.assertRequests(t, "https://pi-in-go.dev/api/latest-version", installLatest,
					installDownload+"/v"+installVersion+"/SHA256SUMS", installDownload+"/v"+installVersion+"/"+f.archive)
			})
		}
	}
}

func TestInstallShRedirectStillRequiresVerifiedArchive(t *testing.T) {
	for _, downloader := range []string{"curl", "wget"} {
		for _, checksum := range []string{"missing", "mismatch", "duplicate"} {
			t.Run(downloader+"/"+checksum, func(t *testing.T) {
				f := newInstallFixture(t)
				f.removeAPI(t)
				f.writeRedirect(t, installLatest, "https://github.com/MichaelKinsy/PiG/releases/tag/v"+installVersion)
				var want string
				switch checksum {
				case "missing":
					if err := os.Remove(filepath.Join(f.release, "SHA256SUMS")); err != nil {
						t.Fatal(err)
					}
					want = "has no SHA256SUMS"
				case "mismatch":
					f.writeSums(t, strings.Repeat("a", 64)+"  "+f.archive)
					want = "checksum mismatch"
				case "duplicate":
					f.writeSums(t, f.archiveSHA+"  "+f.archive, f.archiveSHA+"  ./"+f.archive)
					want = "no single valid entry"
				}
				assertFailure(t, f.run(t, f.downloaderEnv(t, downloader)...), want)
				f.assertNothingInstalled(t)
			})
		}
	}
}

func TestInstallShReleaseLookupRouting(t *testing.T) {
	for _, downloader := range []string{"curl", "wget"} {
		for _, source := range []string{"api", "explicit", "neither", "invalid-redirect", "override"} {
			t.Run(downloader+"/"+source, func(t *testing.T) {
				f := newInstallFixture(t)
				env := f.downloaderEnv(t, downloader)
				var requests []string
				if source != "api" {
					f.removeAPI(t)
				}
				if source == "explicit" {
					env = append(env, "PIG_VERSION=v"+installVersion)
				} else {
					requests = append(requests, "https://pi-in-go.dev/api/latest-version")
				}
				switch source {
				case "neither", "invalid-redirect":
					requests = append(requests, installLatest)
					if source == "invalid-redirect" {
						f.writeRedirect(t, installLatest, "https://github.com/login")
					}
				case "override":
					url := "https://releases.example/latest"
					env = append(env, "PIG_LATEST_RELEASE_URL="+url)
					f.writeRedirect(t, url, "https://github.com/MichaelKinsy/PiG/releases/tag/v"+installVersion)
					requests = append(requests, url)
				}
				got := f.run(t, env...)
				if source == "neither" || source == "invalid-redirect" {
					assertFailure(t, got, "named no release")
					f.assertNothingInstalled(t)
				} else {
					if got.status != 0 {
						t.Fatalf("install = %+v", got)
					}
					requests = append(requests, installDownload+"/v"+installVersion+"/SHA256SUMS", installDownload+"/v"+installVersion+"/"+f.archive)
				}
				f.assertRequests(t, requests...)
			})
		}
	}
}
