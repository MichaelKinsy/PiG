package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

func runSigningCommand(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := RunCommand(append([]string{"piglet"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestSigningCLIKeygenTrustAndVerify(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	keyPath := filepath.Join(t.TempDir(), "author.key")
	code, stdout, stderr := runSigningCommand(t, "keygen", keyPath)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Piglet signing key ed25519:") {
		t.Fatalf("keygen: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	privateBefore, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr = runSigningCommand(t, "keygen", keyPath); code != 1 || !strings.Contains(stderr, "file exists") {
		t.Fatalf("second keygen: code=%d stderr=%q", code, stderr)
	}
	if privateAfter, err := os.ReadFile(keyPath); err != nil || string(privateAfter) != string(privateBefore) {
		t.Fatalf("second keygen changed private key: err=%v", err)
	}

	unsigned := filepath.Join(t.TempDir(), "pig-unsigned")
	if err := os.WriteFile(unsigned, []byte("unsigned Piglet Binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr = runSigningCommand(t, "verify", unsigned); code != 1 || stderr != "" || !strings.Contains(stdout, "Signature: unsigned") {
		t.Fatalf("verify unsigned: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	if code, stdout, stderr = runSigningCommand(t, "trust", "add", keyPath+".pub"); code != 0 || stderr != "" || !strings.Contains(stdout, "Trusted Piglet signer ed25519:") {
		t.Fatalf("trust add: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	private, err := signature.ReadPrivateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signature.Sign(unsigned, signature.Manifest{Piglet: "test", Target: "test/test", PigVersion: "test"}, private); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr = runSigningCommand(t, "verify", unsigned, "--json"); code != 0 || stderr != "" || !strings.Contains(stdout, `"verified": true`) || !strings.Contains(stdout, `"trusted": true`) {
		t.Fatalf("verify signed: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	signed, err := os.ReadFile(unsigned)
	if err != nil {
		t.Fatal(err)
	}
	signed[0] ^= 1
	if err := os.WriteFile(unsigned, signed, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, stdout, stderr = runSigningCommand(t, "verify", unsigned); code != 1 || stderr != "" || !strings.Contains(stdout, "FAILED:") || !strings.Contains(stdout, "executable bytes changed") {
		t.Fatalf("verify tampered: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestSignatureSummaryRejectsManifestForAnotherRecord(t *testing.T) {
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
	if err := os.WriteFile(binary, []byte("executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := signature.Sign(binary, signature.Manifest{
		ReleaseVersion: "2.0.0", Target: "plan9/mips", SourceDigest: "sha256:other",
		ResolutionDigest: "sha256:other", ComponentPlanDigest: "sha256:other",
	}, private); err != nil {
		t.Fatal(err)
	}
	record := RecordInfo{
		ArtifactPath: binary, ReleaseVersion: "1.0.0", Target: "linux/amd64",
		PigletDigest: "sha256:source", ResolutionDigest: "sha256:resolution", ComponentPlanDigest: "sha256:plan",
	}
	if got := signatureSummary(record); !strings.Contains(got, "FAILED: signed manifest names release version") {
		t.Fatalf("signatureSummary() = %q", got)
	}
}

func TestSigningCLIRequireAndRevokePolicies(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	keyPath := filepath.Join(t.TempDir(), "author.key")
	if code, _, stderr := runSigningCommand(t, "keygen", keyPath); code != 0 {
		t.Fatalf("keygen: code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runSigningCommand(t, "trust", "add", keyPath+".pub"); code != 0 {
		t.Fatalf("trust add: code=%d stderr=%q", code, stderr)
	}
	if code, stdout, stderr := runSigningCommand(t, "trust", "require", "on"); code != 0 || stderr != "" || !strings.Contains(stdout, "on") {
		t.Fatalf("trust require: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, stderr := runSigningCommand(t, "trust", "list"); code != 0 || stderr != "" || !strings.Contains(stdout, "Require signature: on") {
		t.Fatalf("trust list: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	private, err := signature.ReadPrivateKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pig-signed")
	if err := os.WriteFile(binary, []byte("executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := signature.Sign(binary, signature.Manifest{Piglet: "test"}, private); err != nil {
		t.Fatal(err)
	}
	status, err := signature.Check(binary, signature.Policy{})
	if err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runSigningCommand(t, "trust", "revoke", status.KeyID); code != 0 || stderr != "" {
		t.Fatalf("trust revoke: code=%d stderr=%q", code, stderr)
	}
	if code, stdout, stderr := runSigningCommand(t, "verify", binary); code != 1 || stderr != "" || !strings.Contains(stdout, "a key revoked") {
		t.Fatalf("verify revoked: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
