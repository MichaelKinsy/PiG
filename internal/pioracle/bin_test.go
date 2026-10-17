package pioracle

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// fakeOracle lays out <dir>/oracle/node_modules/.bin/pi printing version, with an optional build stamp in <dir>/oracle.
func fakeOracle(t *testing.T, version, stamp string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake oracle is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "oracle", "node_modules", ".bin", "pi")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if stamp != "" {
		if err := os.WriteFile(filepath.Join(dir, "oracle", StampFile), []byte(stamp), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

func TestCheckBinAcceptsOnlyThePinnedBuild(t *testing.T) {
	good := `{"commit":"` + pigversion.UpstreamCommit + `","source":"build"}`
	other := `{"commit":"0000000000000000000000000000000000000000","source":"build"}`
	cases := []struct {
		name, version, stamp, want string
	}{
		{"the pinned build", pigversion.UpstreamPackageVersion, good, ""},
		{"another commit of the same package version", pigversion.UpstreamPackageVersion, other, "build of Pi commit"},
		{"a package with no build stamp", pigversion.UpstreamPackageVersion, "", "not the source build"},
		{"another package version", "0.0.1", good, "version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckBin(fakeOracle(t, tc.version, tc.stamp))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("CheckBin = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckBin = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// fakeDurable lays out <dir>/durable-oracle/node_modules with a pi-durable package of version, an optional build stamp in <dir>/durable-oracle,
// and a link to it as durable/interop has.
func fakeDurable(t *testing.T, version, stamp string) string {
	t.Helper()
	dir := t.TempDir()
	modules := filepath.Join(dir, "durable-oracle", "node_modules")
	pkg := filepath.Join(modules, "@earendil-works", "pi-durable")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"@earendil-works/pi-durable","version":"`+version+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if stamp != "" {
		if err := os.WriteFile(filepath.Join(dir, "durable-oracle", StampFile), []byte(stamp), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "interop", "node_modules")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, modules, link)
	return link
}

// A commit pin's Pi Durable has the release's package version, so durable/interop must refuse the published package (what it ran before
// install-oracle built pi-durable, which hid the pi.tool v2 differences) and accept only the stamped build of the pinned commit.
func TestCheckDurableAcceptsOnlyThePinnedBuild(t *testing.T) {
	commitPin := pigversion.UpstreamVersion != pigversion.UpstreamPackageVersion
	good := `{"commit":"` + pigversion.UpstreamCommit + `"}`
	other := `{"commit":"0000000000000000000000000000000000000000"}`
	published := ""
	if commitPin {
		published = "not the source build"
	}
	cases := []struct {
		name, version, stamp, want string
	}{
		{"the pinned build", pigversion.UpstreamPackageVersion, good, ""},
		{"the published package", pigversion.UpstreamPackageVersion, "", published},
		{"another package version", "0.0.1", good, "want " + pigversion.UpstreamPackageVersion},
	}
	if commitPin {
		cases = append(cases, struct{ name, version, stamp, want string }{"another commit of the same package version", pigversion.UpstreamPackageVersion, other, "build of Pi commit"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDurable(fakeDurable(t, tc.version, tc.stamp))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("CheckDurable = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckDurable = %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if err := CheckDurable(t.TempDir()); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("CheckDurable of an empty directory = %v, want not installed", err)
	}
}
