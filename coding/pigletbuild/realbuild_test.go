package pigletbuild

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// componentMarker is a string compiled into the small Piglet's fused
// extension, so tests can change one byte of that component's code.
const componentMarker = "pigletsmall-component-marker"

// writeSmallPiglet writes the smallest buildable Piglet: one fused Go factory
// extension. It returns the Piglet source path.
func writeSmallPiglet(t *testing.T, root string) string {
	t.Helper()
	extension := filepath.Join(root, "hello")
	if err := os.MkdirAll(extension, 0o755); err != nil {
		t.Fatal(err)
	}
	source := "package hello\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension {\n\text := sdk.New(\"hello\")\n\text.Command(\"marker\", \"" + componentMarker + "\", func(sdk.Context, string) error { return nil })\n\treturn ext\n}\n"
	files := map[string]string{
		filepath.Join(extension, "go.mod"):       "module example.com/pigletsmall/hello\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n",
		filepath.Join(extension, "extension.go"): source,
		filepath.Join(root, "small.yaml"):        "name: small\nextensions:\n  - name: hello\n    origins: [local:./hello]\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "small.yaml")
}

// buildSmallPigletBinary runs a real `pig piglet build --format binary` of
// the small Piglet with HOME and PIG_HOME isolated, and returns the artifact
// path.
func buildSmallPigletBinary(t *testing.T, extraArgs ...string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "home"))
	t.Setenv("HOME", filepath.Join(root, "home"))
	// Go makes downloaded module-cache files read-only by default, which would
	// prevent testing.TempDir from removing this isolated HOME on Unix.
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -modcacherw"))
	name := "pig-small"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	artifact := filepath.Join(root, name)
	args := append([]string{writeSmallPiglet(t, root), "--format", "binary", "--builder", "native", "--out", artifact}, extraArgs...)
	var stdout, stderr strings.Builder
	if code := runBuild(args, &stdout, &stderr); code != 0 {
		t.Fatalf("pig piglet build exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	phases := []string{"Resolving manifest", "Resolving extensions", "Packing resources", "Selecting builder", "Locking build inputs", "Preparing fused Go members", "Checking fused Go members", "Packing build inputs", "Compiling and linking Go binary"}
	if hasBuildFlag(extraArgs, "--sign-key") {
		phases = append(phases, "Signing binary")
	}
	phases = append(phases, "Checksumming and verifying binary", "Writing binary and records", "Built "+artifact)
	assertBuildPhaseOrder(t, stderr.String(), phases)
	if strings.Contains(stderr.String(), "Checksumming embedded cells") {
		t.Fatal("fused-only build reports checksumming cells that do not exist")
	}
	info, err := os.Stat(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("(%d bytes) in ", info.Size()); !strings.Contains(stderr.String(), want) {
		t.Fatalf("missing summary %q: %s", want, &stderr)
	}
	return artifact
}

// startPigletBinary runs the binary through its startup verification to a
// command that exits without any model call, and returns the exit code (-1
// when the OS killed it) and combined output.
func startPigletBinary(t *testing.T, path string, args ...string) (int, string) {
	t.Helper()
	if len(args) == 0 {
		args = []string{"--offline", "--list-models"}
	}
	output, err := exec.Command(path, args...).CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, string(output)
	case errors.As(err, &exit):
		return exit.ExitCode(), string(output)
	default:
		t.Fatalf("run %s: %v", path, err)
		return 0, ""
	}
}

func TestNativePigletBinaryBuildLeavesSourceTreeUntouched(t *testing.T) {
	sourceRoot, err := pigSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	staged := []string{
		"go.mod",
		filepath.Join("coding", "extension", "host", "fusepack", "registry_generated.go"),
		filepath.Join("coding", "extension", "host", "cellpack", "cells", "manifest.json"),
		filepath.Join(bakedPigletDir, "piglet.yaml"),
		filepath.Join(bakedPigletDir, "resolution-record.json"),
		filepath.Join(bakedPigletDir, "signers.txt"),
	}
	before := make(map[string][]byte, len(staged))
	for _, relative := range staged {
		data, err := os.ReadFile(filepath.Join(sourceRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		before[relative] = data
	}

	artifact := buildSmallPigletBinary(t)

	for _, relative := range staged {
		after, err := os.ReadFile(filepath.Join(sourceRoot, relative))
		if err != nil || !bytes.Equal(after, before[relative]) {
			t.Fatalf("build wrote %s into the source tree (err=%v)", relative, err)
		}
	}
	binary, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(binary, []byte(`{"kind":"piglet-resolution","piglet":"small"`)) {
		t.Fatal("built binary does not embed the overlaid resolution record")
	}
	if code, output := startPigletBinary(t, artifact); code != 0 {
		t.Fatalf("unsigned Piglet Binary did not start: exit %d\n%s", code, output)
	}
	status, err := signature.Check(artifact, signature.Policy{RequireKnownSigner: true})
	if err != nil || status.Signed {
		t.Fatalf("unsigned build signature status = %+v, %v", status, err)
	}
	var stdout, stderr strings.Builder
	if code := piglet.RunCommand([]string{"piglet", "verify", artifact}, &stdout, &stderr); code != 1 || !strings.Contains(stdout.String(), "Signature: unsigned") {
		t.Fatalf("pig piglet verify on an unsigned build: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := piglet.RunCommand([]string{"piglet", "show", "small", "--json"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), `"signature": "unsigned`) {
		t.Fatalf("pig piglet show on an unsigned build: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
}

// A signed Piglet Binary starts, names its signer, and refuses to start when
// any byte of a component, its record, its Piglet, or its signature changes,
// when the signature is stripped, or when another key re-signs it.
func TestSignedPigletBinaryRefusesAlteration(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "author.key")
	keyID, err := signature.GenerateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	artifact := buildSmallPigletBinary(t, "--sign-key", keyPath)
	if code, output := startPigletBinary(t, artifact); code != 0 {
		t.Fatalf("signed Piglet Binary did not start: exit %d\n%s", code, output)
	}
	var stdout, stderr strings.Builder
	if code := piglet.RunCommand([]string{"piglet", "verify", artifact}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Signature: signed by "+keyID) {
		t.Fatalf("pig piglet verify: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := piglet.RunCommand([]string{"piglet", "show", "small", "--json"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), `"signature": "signed by `+keyID) {
		t.Fatalf("pig piglet show signer status: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	signed, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	author, err := signature.ReadPrivateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	status, err := signature.Check(artifact, signature.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	executable := signed[:status.Manifest.Executable.Size]
	for name, tampered := range map[string][]byte{
		"component": flipInside(t, signed, []byte(componentMarker)),
		"record":    flipInside(t, signed, []byte(`{"kind":"piglet-resolution","piglet":"small"`)),
		"piglet":    flipInside(t, signed, []byte("name: small\n")),
		"signature": flipAt(signed, len(signed)-40),
		"footer":    flipAt(signed, len(signed)-1),
		"stripped":  executable,
	} {
		t.Run(name, func(t *testing.T) {
			assertRefuses(t, writeExecutable(t, tampered), author, "")
		})
	}
	t.Run("signature before command dispatch", func(t *testing.T) {
		path := writeExecutable(t, flipAt(signed, len(signed)-40))
		assertRefusesCommand(t, path, author, "", "version")
	})
	t.Run("wrong key", func(t *testing.T) {
		path := writeExecutable(t, executable)
		_, attacker, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := signature.Sign(path, status.Manifest, attacker); err != nil {
			t.Fatal(err)
		}
		assertRefuses(t, path, author, "neither its author's embedded key nor in your Piglet trust store")
	})
}

// flipInside changes one byte inside the first occurrence of marker.
func flipInside(t *testing.T, data, marker []byte) []byte {
	t.Helper()
	index := bytes.Index(data, marker)
	if index < 0 {
		t.Fatalf("built binary does not contain %q", marker)
	}
	return flipAt(data, index+len(marker)-2)
}

func flipAt(data []byte, offset int) []byte {
	out := bytes.Clone(data)
	out[offset] ^= 0x01
	return out
}

func writeExecutable(t *testing.T, data []byte) string {
	t.Helper()
	name := "pig-small"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertRefuses proves that the author-keyed signature check rejects path and
// that the binary does not start. macOS kills an ad-hoc-signed Mach-O whose
// code pages changed before Go runs, so an OS kill also counts as refusal
// there; an ordinary exit must carry the Piglet signature error.
func assertRefuses(t *testing.T, path string, author ed25519.PrivateKey, want string) {
	t.Helper()
	assertRefusesCommand(t, path, author, want, "--offline", "--list-models")
}

func assertRefusesCommand(t *testing.T, path string, author ed25519.PrivateKey, want string, args ...string) {
	t.Helper()
	policy := signature.Policy{Embedded: []ed25519.PublicKey{author.Public().(ed25519.PublicKey)}, RequireKnownSigner: true}
	if _, err := signature.Check(path, policy); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("signature check error = %v, want %q", err, want)
	}
	code, output := startPigletBinary(t, path, args...)
	if code == -1 && runtime.GOOS == "darwin" {
		return
	}
	if code != 1 || !strings.Contains(output, "pig: Piglet Binary verification failed:") || !strings.Contains(output, want) {
		t.Fatalf("altered Piglet Binary: exit %d, output:\n%s", code, output)
	}
}
