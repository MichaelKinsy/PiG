package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Upstream getShellConfig never reads $SHELL: a zsh or fish login shell must
// not run the bash tool's commands.
func TestGetShellConfig_IgnoresSHELL(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "zsh")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", fake)
	cfg, err := GetShellConfig(nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if cfg.Path == fake {
		t.Fatalf("GetShellConfig used $SHELL %q; upstream resolves bash", fake)
	}
	want, err := defaultShellConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != want.Path {
		t.Fatalf("path = %q, want platform default %q", cfg.Path, want.Path)
	}
}

func TestGetShellConfig_MissingCustomPathErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope", "bash")
	_, err := GetShellConfig(fakeSettings{path: missing})
	if err == nil || err.Error() != "Custom shell path not found: "+missing {
		t.Fatalf("err = %v, want upstream's custom-path error", err)
	}
}

func TestGetShellConfig_CustomPathUsesDashC(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "mybash")
	if err := os.WriteFile(custom, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg, err := GetShellConfig(fakeSettings{path: custom})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Path != custom || strings.Join(cfg.Args, " ") != "-c" || cfg.CommandTransport != "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

// A stdin-transport shell receives the command on stdin, not in argv.
func TestExecuteBashStdinTransport(t *testing.T) {
	sh, err := defaultShellConfig()
	if err != nil {
		t.Fatal(err)
	}
	res, err := ExecuteBash(context.Background(), "echo via-stdin", t.TempDir(),
		ShellConfig{Path: sh.Path, Args: []string{"-s"}, CommandTransport: "stdin"}, BashExecOptions{})
	if err != nil || res.ExitCode == nil || *res.ExitCode != 0 {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if strings.TrimSpace(res.Output) != "via-stdin" {
		t.Fatalf("output = %q", res.Output)
	}
}

// The bash tool surfaces the missing custom shell as the call's error.
func TestBashToolMissingCustomShell(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "bash")
	bt := &BashTool{CWD: t.TempDir(), Settings: fakeSettings{path: missing}}
	args, _ := json.Marshal(bashParams{Command: "echo hi"})
	res, err := bt.Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || res.Content != "Custom shell path not found: "+missing {
		t.Fatalf("res = %+v", res)
	}
}
