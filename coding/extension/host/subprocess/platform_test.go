package subprocess

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtensionArtifactNameByPlatform(t *testing.T) {
	tests := []struct {
		goos, language, want string
	}{
		{"windows", "go", "bin.exe"},
		{"windows", "rust", "bin.exe"},
		{"windows", "node", "bin"},
		{"linux", "go", "bin"},
	}
	for _, test := range tests {
		if got := extensionArtifactName(test.goos, test.language); got != test.want {
			t.Errorf("extensionArtifactName(%q, %q) = %q, want %q", test.goos, test.language, got, test.want)
		}
	}
}

func TestWindowsPythonLauncherUsesPythonExeWithoutShebang(t *testing.T) {
	lookPath := func(name string) (string, error) {
		switch name {
		case "python":
			return `C:\Python312\python.exe`, nil
		default:
			return "", errors.New("not found")
		}
	}
	cmd := buildExtCommandForGOOS(context.Background(), `C:\cache\runner.py`, "python", "windows", lookPath)
	if cmd.Path != `C:\Python312\python.exe` {
		t.Fatalf("Windows Python command = %q, want python.exe", cmd.Path)
	}
	if got := cmd.Args[1:]; len(got) != 1 || got[0] != `C:\cache\runner.py` {
		t.Fatalf("Windows Python args = %q", got)
	}
}

func TestWindowsPythonPathIsCaseInsensitiveAndUsesWindowsSeparator(t *testing.T) {
	got := prependUniquePathForGOOS("windows", `C:\SDK`, `c:\sdk;C:\User`)
	if got != `C:\SDK;C:\User` {
		t.Fatalf("Windows PYTHONPATH = %q, want case-insensitive deduplication", got)
	}
	if !envHasKey(`PythonPath=C:\User`, "PYTHONPATH") {
		t.Fatal("mixed-case Windows PYTHONPATH key was not recognized")
	}
}

func TestWindowsSocketDirectoryDoesNotUseUnixUID(t *testing.T) {
	dir := resolveSocketDirForGOOS("windows", `/run/user/1000`, `/unix/tmp`, `C:\Users\runner\AppData\Local\Temp`, 1000)
	if strings.Contains(dir, "1000") || !strings.HasSuffix(filepath.ToSlash(dir), "/pig") {
		t.Fatalf("Windows socket directory = %q, want per-user temp pig directory without uid", dir)
	}
}

func TestValidateWindowsUnixSocketPathLimit(t *testing.T) {
	if err := validateUnixSocketPath("windows", strings.Repeat("x", 107)); err != nil {
		t.Fatalf("107-byte Windows AF_UNIX path rejected: %v", err)
	}
	if err := validateUnixSocketPath("windows", strings.Repeat("x", 108)); err == nil {
		t.Fatal("108-byte Windows AF_UNIX path accepted without room for NUL terminator")
	}
}
