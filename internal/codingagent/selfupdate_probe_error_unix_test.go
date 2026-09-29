//go:build unix

package codingagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi config.ts:204-206,226-228 surfaces an explicit npm command's failed root probe. D39 still chooses one proven native owner; an unrelated npm failure cannot override another proven owner.
func TestConfiguredNpmProbeFailurePreservesActionableErrorAndOwner(t *testing.T) {
	for _, mode := range []string{"configured npm", "configured wrapper", "unconfigured npm", "pnpm owner", "yarn owner", "bun owner", "standalone owner"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "calls")
			script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nprintf 'configured-probe-denied' >&2\nexit 23\n", log)
			for _, name := range []string{"npm", "npm-wrapper"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			t.Setenv("HOME", root)
			t.Setenv("PIG_INSTALL_TIER", "")
			dir := filepath.Join(root, "lib", "node_modules", "@earendil-works", "pi-coding-agent", "dist")
			if mode == "bun owner" {
				dir = filepath.Join(root, ".bun", "install", "global", "node_modules", "@earendil-works", "pi-coding-agent", "dist")
			}
			if mode == "pnpm owner" || mode == "yarn owner" {
				manager := strings.TrimSuffix(mode, " owner")
				global := filepath.Join(root, "owned")
				dir = filepath.Join(global, "node_modules", "pi-coding-agent", "dist")
				if manager == "pnpm" {
					global = filepath.Join(global, "node_modules")
				}
				probe := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %q\n", global)
				if err := os.WriteFile(filepath.Join(bin, manager), []byte(probe), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "standalone owner" {
				dir = filepath.Join(root, "standalone")
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			exe := writeFakeExe(t, dir)
			if mode == "standalone owner" {
				writeStandaloneTestReceipt(t, exe)
			}
			command := []string{"npm", "--wrapper-option"}
			if mode == "configured wrapper" {
				command[0] = "npm-wrapper"
			}
			if mode == "unconfigured npm" {
				command = nil
			}
			provenance, err := resolveTierForExe(t, exe, osCmdRunner{npmCommand: command, ctx: t.Context()})
			called, readErr := os.ReadFile(log)
			wantCall := "--wrapper-option root -g\n"
			if mode == "unconfigured npm" {
				wantCall = "root -g\n"
			}
			if readErr != nil || string(called) != wantCall {
				t.Fatalf("probe must run once with configured args: calls=%q error=%v", called, readErr)
			}
			if mode == "configured npm" || mode == "configured wrapper" {
				message := "Failed to run " + command[0] + " --wrapper-option root -g: configured-probe-denied"
				if err == nil || err.Error() != message || provenance != nil {
					t.Fatalf("provenance=%+v error=%v; want %q", provenance, err, message)
				}
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 23 || !strings.Contains(string(exit.Stderr), "configured-probe-denied") {
					t.Fatalf("original command failure lost: %v", err)
				}
				return
			}
			if err != nil || provenance == nil {
				t.Fatalf("provenance=%+v error=%v", provenance, err)
			}
			wantTier := TierUnsupported
			switch mode {
			case "bun owner", "pnpm owner", "yarn owner":
				wantTier = TierPackageManager
				owner := PackageManagerOwner(strings.TrimSuffix(mode, " owner"))
				if provenance.PackageOwner != owner {
					t.Fatalf("owner=%s; want %s", provenance.PackageOwner, owner)
				}
			case "standalone owner":
				wantTier = TierStandalone
			}
			if provenance.Tier != wantTier {
				t.Fatalf("tier=%s; want %s", provenance.Tier, wantTier)
			}
		})
	}
}

func BenchmarkConfiguredNpmProbeFailure(b *testing.B) {
	wrapper := filepath.Join(b.TempDir(), "npm-wrapper")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nprintf 'probe denied' >&2\nexit 23\n"), 0o700); err != nil {
		b.Fatal(err)
	}
	b.Setenv("PIG_INSTALL_TIER", "")
	b.Setenv("HOME", b.TempDir())
	b.Setenv("PATH", b.TempDir())
	runner := osCmdRunner{npmCommand: []string{wrapper, "--wrapper-option"}, ctx: b.Context()}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := resolveSelfUpdateTierForExe(wrapper, runner); err == nil {
			b.Fatal("missing configured probe failure")
		}
	}
}
