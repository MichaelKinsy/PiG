package pigletbuild

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// markerDescriptions starts the Piglet Binary in RPC mode with args and returns the description of every command whose name starts
// with "marker", as get_commands lists them, with everything the run wrote.
func markerDescriptions(t *testing.T, binary, path string, args ...string) ([]string, string) {
	t.Helper()
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), binary, append([]string{"--mode", "rpc", "--no-session", "--offline"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(),
		"PATH="+path,
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"PIG_HOME="+filepath.Join(home, ".pig"),
		"PIG_CODING_AGENT_DIR="+filepath.Join(home, ".pig", "agent"),
		"PI_CODING_AGENT_DIR="+filepath.Join(home, ".pi", "agent"),
	)
	cmd.Stdin = strings.NewReader(`{"id":"commands","type":"get_commands"}` + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %q: %v\nstdout:\n%s\nstderr:\n%s", binary, args, err, &stdout, &stderr)
	}
	all := stdout.String() + stderr.String()
	var descriptions []string
	answered := false
	for line := range strings.SplitSeq(stdout.String(), "\n") {
		var response struct {
			Command string `json:"command"`
			Data    struct {
				Commands []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"commands"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &response) != nil || response.Command != "get_commands" {
			continue
		}
		answered = true
		for _, command := range response.Data.Commands {
			if strings.HasPrefix(command.Name, "marker") {
				descriptions = append(descriptions, command.Name+": "+command.Description)
			}
		}
	}
	if !answered {
		t.Fatalf("no get_commands response\nstdout:\n%s\nstderr:\n%s", &stdout, &stderr)
	}
	return descriptions, all
}

// A real Piglet Binary built with `build.fusedReload: source` (D31), given its fused member's source by `-e`: with the source it was
// built from it runs the fused copy and needs no toolchain; after an edit it runs the member once, from the edited source; and
// with the edit but no Go toolchain it runs the fused copy and warns. A Binary built without the key loads the source beside the
// fused member, as before.
func TestPigletBinaryRunsAnEditedFusedMemberFromItsSource(t *testing.T) {
	if testing.Short() {
		t.Skip("builds Piglet Binaries")
	}
	const warning = "differs from the fused copy; no Go toolchain: running the fused copy"
	path := os.Getenv("PATH")
	edit := func(t *testing.T, member string) {
		t.Helper()
		file := filepath.Join(member, "extension.go")
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, bytes.ReplaceAll(source, []byte(componentMarker), []byte("edited-description")), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("with the key", func(t *testing.T) {
		binary := buildSmallPigletBinaryWith(t, "build:\n  fusedReload: source\n")
		member := filepath.Join(filepath.Dir(binary), "hello")
		fusedCopy := []string{"marker: " + componentMarker}

		if got, output := markerDescriptions(t, binary, path); !slices.Equal(got, fusedCopy) {
			t.Fatalf("without -e: %q\n%s", got, output)
		}
		// The source the Binary was built from: the fused copy, with no toolchain on PATH and no warning.
		if got, output := markerDescriptions(t, binary, "", "-e", member); !slices.Equal(got, fusedCopy) || strings.Contains(output, warning) {
			t.Fatalf("unchanged source: %q\n%s", got, output)
		}
		edit(t, member)
		if got, output := markerDescriptions(t, binary, path, "-e", member); !slices.Equal(got, []string{"marker: edited-description"}) {
			t.Fatalf("edited source: %q, want the one member from its source\n%s", got, output)
		}
		got, output := markerDescriptions(t, binary, "", "-e", member)
		if !slices.Equal(got, fusedCopy) || strings.Count(output, warning) != 1 {
			t.Fatalf("edited source without a toolchain: %q and %d warnings, want the fused copy and one\n%s", got, strings.Count(output, warning), output)
		}
		if !strings.Contains(output, `the "go" toolchain is not installed; install it`) {
			t.Fatalf("the warning does not carry the builder's remedy:\n%s", output)
		}
	})

	t.Run("without the key", func(t *testing.T) {
		binary := buildSmallPigletBinary(t)
		member := filepath.Join(filepath.Dir(binary), "hello")
		edit(t, member)
		// The source copy packs with the SDK of this checkout, as it did before the key existed.
		sourceRoot, err := pigSourceRoot()
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(sourceRoot, "extensions", "sdk"))
		got, output := markerDescriptions(t, binary, path, "-e", member)
		// Two copies, as before the key: the fused member and the source beside it, their commands numbered.
		if want := []string{"marker:1: " + componentMarker, "marker:2: edited-description"}; !slices.Equal(got, want) || strings.Contains(output, "fused copy") {
			t.Fatalf("without the key: %q, want %q\n%s", got, want, output)
		}
	})
}
