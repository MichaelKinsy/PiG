package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

func TestSetupStatusNamesTheSetupCommandForMissingGo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := runSetupCommand([]string{"setup"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"go         missing", "container  missing", "Next steps:", "pig setup go", "pig setup container"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output lacks %q:\n%s", want, out)
		}
	}
	if code := runSetupCommand([]string{"setup", "rust"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown topic exit = %d, want 2", code)
	}
	if code := runSetupCommand([]string{"status"}, &stdout, &stderr); code != -1 {
		t.Fatalf("non-setup command claimed with %d", code)
	}
}

func TestSetupContainerGuideCoversEachOperatingSystem(t *testing.T) {
	for goos, want := range map[string]string{"linux": "sudo dnf install podman", "darwin": "podman machine init", "windows": "winget install Docker.DockerDesktop"} {
		if guide := containerGuide(goos); !strings.Contains(guide, want) || !strings.Contains(guide, "PIG_BUILDERS_FILE") {
			t.Fatalf("%s guide lacks %q or the builder-image note:\n%s", goos, want, guide)
		}
	}
}

func runVerify(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	if os.Getenv("PIG_HOME") == "" {
		t.Setenv("PIG_HOME", t.TempDir())
	}
	var stdout, stderr bytes.Buffer
	code := runVerifyCommand(append([]string{"verify"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestVerifyReportsTheBinaryDigestAndIdentity(t *testing.T) {
	code, out, errOut := runVerify(t, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var report verifyReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if !report.Verified || report.Identity.PigVersion == "" || report.Identity.Upstream == "" || len(report.Targets) != 1 || report.Targets[0].Kind != "binary" || len(report.Targets[0].SHA256) != 64 {
		t.Fatalf("report = %+v", report)
	}
}

func TestVerifyChecksumsRequireAListedMatchingDigest(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "pig-1.0.0-linux-amd64.tar.gz")
	if err := os.WriteFile(artifact, []byte("archive bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := fileSHA256(artifact)
	if err != nil {
		t.Fatal(err)
	}
	sums := filepath.Join(dir, "SHA256SUMS")
	write := func(content string) {
		if err := os.WriteFile(sums, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(digest + "  pig-1.0.0-linux-amd64.tar.gz\n")
	if code, out, _ := runVerify(t, "--checksums", sums, artifact); code != 0 || !strings.Contains(out, "ok   checksum") {
		t.Fatalf("matching digest: exit %d\n%s", code, out)
	}
	write(strings.Repeat("0", 64) + "  pig-1.0.0-linux-amd64.tar.gz\n")
	if code, out, _ := runVerify(t, "--checksums", sums, artifact); code != 1 || !strings.Contains(out, "digest differs") {
		t.Fatalf("mismatched digest: exit %d\n%s", code, out)
	}
	write(digest + "  some-other-file\n")
	if code, out, _ := runVerify(t, "--checksums", sums, artifact); code != 1 || !strings.Contains(out, "is not listed") {
		t.Fatalf("unlisted file: exit %d\n%s", code, out)
	}
	write("not a checksum line\n")
	if code, _, errOut := runVerify(t, "--checksums", sums, artifact); code != 1 || !strings.Contains(errOut, "not a SHA-256 checksum line") {
		t.Fatalf("malformed checksums: exit %d\n%s", code, errOut)
	}
}

func TestVerifyReportsPigletSignatureStatusAndRefusesTampering(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	keyPath := filepath.Join(t.TempDir(), "author.key")
	if _, err := signature.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	private, err := signature.ReadPrivateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pig-signed")
	if err := os.WriteFile(binary, []byte("executable bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runVerify(t, binary); code != 0 || errOut != "" || !strings.Contains(out, "n/a  piglet signature: unsigned") {
		t.Fatalf("unsigned: exit %d stdout=%q stderr=%q", code, out, errOut)
	}
	if _, err := signature.Sign(binary, signature.Manifest{Piglet: "test"}, private); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runVerify(t, binary); code != 0 || errOut != "" || !strings.Contains(out, "ok   piglet signature: signed by") {
		t.Fatalf("signed: exit %d stdout=%q stderr=%q", code, out, errOut)
	}
	signed, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	signed[0] ^= 1
	if err := os.WriteFile(binary, signed, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runVerify(t, binary); code != 1 || errOut != "" || !strings.Contains(out, "FAIL piglet signature") || !strings.Contains(out, "executable bytes changed") {
		t.Fatalf("tampered: exit %d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestVerifyAppliesPigletRequireSignaturePolicyToSignedFiles(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	if err := signature.SetRequireSignature(signature.TrustDir(), true); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(binary, []byte("unsigned"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runVerify(t, binary); code != 0 || errOut != "" || !strings.Contains(out, "n/a  piglet signature: unsigned") {
		t.Fatalf("unsigned generic file: exit %d stdout=%q stderr=%q", code, out, errOut)
	}
	keyPath := filepath.Join(t.TempDir(), "author.key")
	if _, err := signature.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	private, err := signature.ReadPrivateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signature.Sign(binary, signature.Manifest{Piglet: "test"}, private); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runVerify(t, binary); code != 1 || errOut != "" || !strings.Contains(out, "policy requires a trusted signature") {
		t.Fatalf("required trusted signature: exit %d stdout=%q stderr=%q", code, out, errOut)
	}
}

func TestVerifyProvenanceWithoutGhNamesTheExactCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	artifact := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(artifact, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runVerify(t, "--provenance", artifact)
	if code != 0 || !strings.Contains(out, "n/a  provenance") || !strings.Contains(out, "gh attestation verify "+artifact+" --repo MichaelKinsy/PiG --signer-workflow MichaelKinsy/PiG/.github/workflows/release-candidate.yml") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if code, _, _ := runVerify(t, "--repo"); code != 2 {
		t.Fatalf("--repo without a value exit = %d, want 2", code)
	}
	workflow := "acme/reviewer/.github/workflows/piglet-release.yml"
	code, out, _ = runVerify(t, "--provenance", "--repo", "acme/reviewer", "--signer-workflow", workflow, artifact)
	if code != 0 || !strings.Contains(out, "--signer-workflow "+workflow) {
		t.Fatalf("custom signer workflow: exit %d\n%s", code, out)
	}
}
