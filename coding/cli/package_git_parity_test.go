//go:build parity

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi package-manager.ts:1830-1957 reconciles Git refs and repairs dependencies instead of pulling and reinstalling unchanged trees.
func TestGitPackageCLIComparedWithPi(t *testing.T) {
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pig := buildPigBinaryForSignalTest(t)
	pi := filepath.Join(repo, "extensions/sdk-ts/node_modules/.bin/pi")
	remote := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(remote, "init", "-q", "-b", "main")
	writeManifest := func(version string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(remote, "package.json"), []byte(`{"name":"repo","version":"`+version+`","dependencies":{"dependency":"1.0.0"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		git(remote, "add", "package.json")
		git(remote, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", version)
	}
	writeManifest("1.0.0")
	git(remote, "tag", "v1")
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[url \"file://"+filepath.ToSlash(remote)+"\"]\n insteadOf = https://fixture.invalid/owner/repo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(t.TempDir(), "npm.cjs")
	if err := os.WriteFile(shim, []byte(`const fs=require('node:fs'); console.log('dependency stdout'); console.error('dependency stderr'); if(process.env.FAIL_INSTALL==='1') process.exit(19); fs.mkdirSync('node_modules/dependency',{recursive:true});`), 0o600); err != nil {
		t.Fatal(err)
	}
	type cli struct{ binary, home, cwd, agent string }
	makeCLI := func(binary string) cli {
		t.Helper()
		home := t.TempDir()
		c := cli{binary, home, filepath.Join(home, "work"), filepath.Join(home, "agent")}
		for _, dir := range []string{c.cwd, c.agent} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		settings, err := json.Marshal(map[string]any{"npmCommand": []string{"node", shim}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(c.agent, "settings.json"), settings, 0o600); err != nil {
			t.Fatal(err)
		}
		return c
	}
	p, g := makeCLI(pi), makeCLI(pig)
	run := func(c cli, fail bool, args []string) (string, string, int) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), c.binary, append(args, "--approve")...)
		cmd.Dir = c.cwd
		failure := "0"
		if fail {
			failure = "1"
		}
		cmd.Env = append(os.Environ(), "HOME="+c.home, "PIG_HOME="+filepath.Join(c.home, ".pig"), "PIG_CODING_AGENT_DIR="+c.agent, "PI_CODING_AGENT_DIR="+c.agent, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+config, "PIG_OFFLINE=", "PI_OFFLINE=", "NO_COLOR=1", "FORCE_COLOR=0", "FAIL_INSTALL="+failure)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			if e, ok := errors.AsType[*exec.ExitError](err); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		return strings.ReplaceAll(stdout.String(), c.home, "<HOME>"), strings.ReplaceAll(stderr.String(), c.home, "<HOME>"), code
	}
	pair := func(fail bool, args ...string) {
		t.Helper()
		wo, we, wc := run(p, fail, args)
		go_, ge, gc := run(g, fail, args)
		if go_ != wo || ge != we || gc != wc {
			t.Errorf("%v: pig=(%d,%q,%q), pi=(%d,%q,%q)", args, gc, go_, ge, wc, wo, we)
		}
	}
	const source = "git:https://fixture.invalid/owner/repo"
	pair(false, "install", source)
	pair(false, "update", source)
	writeManifest("2.0.0")
	pair(false, "update", source)
	pair(false, "update", source)
	for _, c := range []cli{p, g} {
		if err := os.RemoveAll(filepath.Join(c.agent, "git/fixture.invalid/owner/repo/node_modules")); err != nil {
			t.Fatal(err)
		}
	}
	pair(false, "update", source)
	pair(false, "install", source+"@v1")
	pair(false, "update", source+"@v1")
	pair(false, "uninstall", source)
	pair(true, "install", source)
	for _, c := range []cli{p, g} {
		if _, err := os.Stat(filepath.Join(c.agent, "git/fixture.invalid/owner/repo")); !os.IsNotExist(err) {
			t.Errorf("failed install leaves checkout for %s: %v", c.binary, err)
		}
	}
	// A nonexistent local ref makes clone fail without network or credential access.
	pair(false, "install", source+"-missing")
}
