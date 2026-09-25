package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestManagedPigPaths(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent-state")
	cwd := filepath.Join(root, "workspace")
	t.Setenv(ENV_AGENT_DIR, agentDir)
	t.Setenv("PIG_HOME", root)

	if got := AgentDir(); got != agentDir {
		t.Fatalf("AgentDir() = %q, want %q", got, agentDir)
	}
	if got := ProjectConfigDir(cwd); got != filepath.Join(cwd, ".pig") {
		t.Fatalf("ProjectConfigDir() = %q", got)
	}
	if got := NPMInstallRoot(cwd, agentDir, false); got != filepath.Join(agentDir, "npm") {
		t.Fatalf("user NPMInstallRoot() = %q", got)
	}
	if got := NPMInstallRoot(cwd, agentDir, true); got != filepath.Join(cwd, ".pig", "npm") {
		t.Fatalf("project NPMInstallRoot() = %q", got)
	}
	if got := GitInstallRoot(cwd, agentDir, false); got != filepath.Join(agentDir, "git") {
		t.Fatalf("user GitInstallRoot() = %q", got)
	}
	if got := GitInstallRoot(cwd, agentDir, true); got != filepath.Join(cwd, ".pig", "git") {
		t.Fatalf("project GitInstallRoot() = %q", got)
	}
	if got := CatalogRoot(agentDir); got != filepath.Join(agentDir, "catalog") {
		t.Fatalf("CatalogRoot() = %q", got)
	}
	if got := StateDir("marketplace"); got != filepath.Join(root, "state", "marketplace") {
		t.Fatalf("StateDir() = %q", got)
	}
	if got := ProjectStateDir(cwd, "marketplace"); got != filepath.Join(cwd, ".pig", "state", "marketplace") {
		t.Fatalf("ProjectStateDir() = %q", got)
	}
	if got := PigletArtifactsDir(); got != filepath.Join(root, "artifacts", "piglets") {
		t.Fatalf("PigletArtifactsDir() = %q", got)
	}
	if got := PigletRecordsDir(); got != filepath.Join(root, "receipts", "piglets") {
		t.Fatalf("PigletRecordsDir() = %q", got)
	}
}

func TestStateDirsRejectPathNamespaces(t *testing.T) {
	for _, namespace := range []string{"", ".", "..", "../marketplace", "marketplace/cache", `marketplace\\cache`} {
		t.Run(namespace, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("StateDir(%q) did not panic", namespace)
				}
			}()
			_ = StateDir(namespace)
		})
	}
}

func TestPackageManagerSelfUpdateCommand_UsesIgnoreScripts(t *testing.T) {
	tests := []struct {
		name       string
		npmCommand []string
		wantCmd    string
		wantArgs   []string
		wantStep0  []string
	}{
		{
			name:     "default npm",
			wantCmd:  "npm",
			wantArgs: []string{"install", "-g", "--ignore-scripts", "--min-release-age=0", "pig"},
		},
		{
			name:       "configured npm args",
			npmCommand: []string{"npm", "--userconfig", "/tmp/npmrc"},
			wantCmd:    "npm",
			wantArgs:   []string{"--userconfig", "/tmp/npmrc", "install", "-g", "--ignore-scripts", "--min-release-age=0", "pig"},
		},
		{
			name:       "pnpm",
			npmCommand: []string{"pnpm"},
			wantCmd:    "pnpm",
			wantArgs:   []string{"install", "-g", "--ignore-scripts", "--config.minimumReleaseAge=0", "pig"},
		},
		{
			name:       "yarn",
			npmCommand: []string{"yarn"},
			wantCmd:    "yarn",
			wantArgs:   []string{"global", "add", "--ignore-scripts", "pig"},
		},
		{
			name:       "bun",
			npmCommand: []string{"bun"},
			wantCmd:    "bun",
			wantArgs:   []string{"install", "-g", "--ignore-scripts", "--minimum-release-age=0", "pig"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := packageManagerSelfUpdateCommand("pig", tt.npmCommand, SelfUpdatePackageTarget{})
			if got == nil {
				t.Fatal("packageManagerSelfUpdateCommand returned nil")
			}
			if got.Command != tt.wantCmd {
				t.Fatalf("Command = %q, want %q", got.Command, tt.wantCmd)
			}
			if strings.Join(got.Args, "\x00") != strings.Join(tt.wantArgs, "\x00") {
				t.Fatalf("Args = %v, want %v", got.Args, tt.wantArgs)
			}
			if !strings.Contains(got.Display, "--ignore-scripts") {
				t.Fatalf("Display = %q, want --ignore-scripts", got.Display)
			}
		})
	}
}

func TestPackageManagerSelfUpdateCommand_RenameIncludesUninstallStep(t *testing.T) {
	got := packageManagerSelfUpdateCommand("old-pig", []string{"pnpm"}, SelfUpdatePackageTarget{PackageName: "pig"})
	if got == nil {
		t.Fatal("packageManagerSelfUpdateCommand returned nil")
	}
	if len(got.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(got.Steps))
	}
	if strings.Join(got.Steps[0].Args, "\x00") != strings.Join([]string{"remove", "-g", "old-pig"}, "\x00") {
		t.Fatalf("Steps[0].Args = %v, want %v", got.Steps[0].Args, []string{"remove", "-g", "old-pig"})
	}
	if strings.Join(got.Steps[1].Args, "\x00") != strings.Join([]string{"install", "-g", "--ignore-scripts", "--config.minimumReleaseAge=0", "pig"}, "\x00") {
		t.Fatalf("Steps[1].Args = %v, want %v", got.Steps[1].Args, []string{"install", "-g", "--ignore-scripts", "pig"})
	}
}

func TestMarkPathIgnoredByCloudSync_InvokesPlatformCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cloud-sync-target")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "calls.log")
	scriptPath := ""
	switch runtime.GOOS {
	case "darwin":
		scriptPath = filepath.Join(binDir, "xattr")
	case "linux":
		scriptPath = filepath.Join(binDir, "setfattr")
	default:
		MarkPathIgnoredByCloudSync(path)
		if _, err := os.Stat(logPath); !os.IsNotExist(err) {
			t.Fatalf("unexpected command invocation log on %s: %v", runtime.GOOS, err)
		}
		return
	}

	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	MarkPathIgnoredByCloudSync(path)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	switch runtime.GOOS {
	case "darwin":
		if len(lines) != 2 {
			t.Fatalf("darwin command count = %d, want 2 (%q)", len(lines), lines)
		}
		for i, attr := range []string{"com.dropbox.ignored", "com.apple.fileprovider.ignore#P"} {
			want := "-w " + attr + " 1 " + path
			if lines[i] != want {
				t.Fatalf("call %d = %q, want %q", i, lines[i], want)
			}
		}
	case "linux":
		want := "-n user.com.dropbox.ignored -v 1 " + path
		if len(lines) != 1 || lines[0] != want {
			t.Fatalf("linux calls = %q, want [%q]", lines, want)
		}
	}
}
