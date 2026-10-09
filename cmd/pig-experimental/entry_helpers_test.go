package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildDevelopmentEntry builds the unshipped pig_experimental entry and returns it with the build's environment. Callers derive isolated run environments from that environment.
func buildDevelopmentEntry(t *testing.T) (binary string, buildEnv []string) {
	t.Helper()
	executable := "pig-experimental"
	goExecutable := "go"
	if runtime.GOOS == "windows" {
		executable += ".exe"
		goExecutable += ".exe"
	}
	goRootOutput, err := exec.CommandContext(t.Context(), "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("resolve GOROOT: %v", err)
	}
	goRoot := strings.TrimSpace(string(goRootOutput))
	goBinary := filepath.Join(goRoot, "bin", goExecutable)
	cacheCommand := exec.CommandContext(t.Context(), goBinary, "env", "-json", "GOCACHE", "GOMODCACHE", "GOPATH")
	cacheOutput, err := cacheCommand.Output()
	if err != nil {
		t.Fatalf("resolve Go caches before HOME isolation: %v", err)
	}
	var caches map[string]string
	if err := json.Unmarshal(cacheOutput, &caches); err != nil {
		t.Fatal(err)
	}

	buildHome := t.TempDir()
	buildEnv = []string{
		"PATH=" + filepath.Dir(goBinary) + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GOROOT=" + goRoot,
		"HOME=" + buildHome,
		"USERPROFILE=" + buildHome,
		"PIG_CODING_AGENT_DIR=" + filepath.Join(buildHome, "pig-agent"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(buildHome, "pi-agent"),
		"PIG_HOME=" + filepath.Join(buildHome, "pig"),
		"PI_HOME=" + filepath.Join(buildHome, "pi"),
		"XDG_CONFIG_HOME=" + filepath.Join(buildHome, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(buildHome, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(buildHome, "data"),
		"XDG_STATE_HOME=" + filepath.Join(buildHome, "state"),
		"TMPDIR=" + buildHome,
		"TMP=" + buildHome,
		"TEMP=" + buildHome,
		"GOTOOLCHAIN=local",
	}
	for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOPATH"} {
		buildEnv = append(buildEnv, key+"="+caches[key])
	}
	for _, key := range []string{"SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(key); ok {
			buildEnv = append(buildEnv, key+"="+value)
		}
	}
	binary = filepath.Join(t.TempDir(), executable)
	build := exec.CommandContext(t.Context(), goBinary, "build", "-tags=pig_experimental", "-o", binary, "./cmd/pig")
	build.Dir = filepath.Join("..", "..")
	build.Env = buildEnv
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build development entrypoint: %v\n%s", err, output)
	}

	return binary, buildEnv
}
