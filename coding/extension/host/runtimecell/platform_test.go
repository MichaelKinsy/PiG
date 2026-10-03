package runtimecell

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPackedRunnerNameByPlatform(t *testing.T) {
	tests := []struct {
		goos, language, want string
	}{
		{"windows", "go", "runner.exe"},
		{"windows", "rust", "runner.exe"},
		{"windows", "python", "runner.py"},
		{"linux", "go", "runner"},
		{"darwin", "rust", "runner"},
	}
	for _, test := range tests {
		if got := packedRunnerName(test.goos, test.language); got != test.want {
			t.Errorf("packedRunnerName(%q, %q) = %q, want %q", test.goos, test.language, got, test.want)
		}
	}
}

func TestPythonPackedRunnerDoesNotDependOnShebangExecution(t *testing.T) {
	runner := renderPythonRunner([]PythonExtension{{
		Name: "portable",
		Root: t.TempDir(), Package: "portable", Factory: "new_extension", Hash: "hash",
	}}, filepath.Join(t.TempDir(), "sdk-py"))
	if strings.HasPrefix(runner, "#!") {
		t.Fatalf("python runner still depends on shebang execution:\n%s", runner)
	}
}
