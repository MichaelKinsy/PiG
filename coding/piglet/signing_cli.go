package piglet

// pig additive (D18): Piglet Binary signing keys, the user's signer trust
// store, and offline signature verification.

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

const trustUsage = `Usage:
  pig piglet trust [list]             Show trusted signer keys, revocations, and policy
  pig piglet trust add <key.pub>      Trust the signer keys in a public key file
  pig piglet trust revoke <key-id>    Revoke a signer key; its signatures are refused
  pig piglet trust require on|off     Require every Piglet Binary to carry a trusted signature
`

func cmdKeygen(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		_, _ = fmt.Fprintln(stderr, "Usage: pig piglet keygen <private-key-path>")
		return 2
	}
	id, err := signature.GenerateKey(args[0])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Piglet signing key %s\n  private: %s (keep it secret; sign with pig piglet build <name> --format binary --sign-key %s)\n  public:  %s.pub (publish it; users trust it with pig piglet trust add)\n",
		id, args[0], args[0], args[0])
	return 0
}

func cmdTrust(args []string, stdout, stderr io.Writer) int {
	dir := signature.TrustDir()
	if len(args) == 0 || args[0] == "list" {
		return listTrust(dir, stdout, stderr)
	}
	var err error
	switch {
	case args[0] == "add" && len(args) == 2:
		var data []byte
		if data, err = os.ReadFile(args[1]); err == nil {
			var ids []string
			if ids, err = signature.TrustKeys(dir, data); err == nil {
				_, _ = fmt.Fprintf(stdout, "Trusted Piglet signer %s\n", strings.Join(ids, ", "))
			}
		}
	case args[0] == "revoke" && len(args) == 2:
		if err = signature.RevokeKey(dir, args[1]); err == nil {
			_, _ = fmt.Fprintf(stdout, "Revoked Piglet signer %s; Piglet Binaries it signed refuse to start\n", args[1])
		}
	case args[0] == "require" && len(args) == 2 && (args[1] == "on" || args[1] == "off"):
		if err = signature.SetRequireSignature(dir, args[1] == "on"); err == nil {
			_, _ = fmt.Fprintf(stdout, "Require a trusted Piglet signature: %s\n", args[1])
		}
	case args[0] == "-h" || args[0] == "--help":
		_, _ = io.WriteString(stdout, trustUsage)
		return 0
	default:
		_, _ = io.WriteString(stderr, trustUsage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func listTrust(dir string, stdout, stderr io.Writer) int {
	trust, err := signature.LoadTrust(dir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	require := "off (unsigned Piglet Binaries run and report unsigned)"
	if trust.RequireSignature {
		require = "on (every Piglet Binary needs a signature by a trusted key)"
	}
	_, _ = fmt.Fprintf(stdout, "Piglet trust store: %s\nRequire signature: %s\n", dir, require)
	_, _ = fmt.Fprintln(stdout, "Trusted signers:")
	if len(trust.Keys) == 0 {
		_, _ = fmt.Fprintln(stdout, "  none")
	}
	for _, id := range trust.SortedKeyIDs() {
		_, _ = fmt.Fprintf(stdout, "  %s\n", id)
	}
	for _, id := range slices.Sorted(maps.Keys(trust.Revoked)) {
		_, _ = fmt.Fprintf(stdout, "Revoked: %s\n", id)
	}
	return 0
}

type pigletVerifyOutput struct {
	Path     string              `json:"path"`
	Verified bool                `json:"verified"`
	Status   string              `json:"status"`
	Signed   bool                `json:"signed"`
	Signer   string              `json:"signer,omitempty"`
	Trusted  bool                `json:"trusted"`
	Manifest *signature.Manifest `json:"manifest,omitempty"`
	Error    string              `json:"error,omitempty"`
}

// cmdVerify checks one Piglet Binary's signature against the user's trust
// store without running it. It succeeds only when a signature verifies; an
// unsigned binary has nothing to verify.
func cmdVerify(args []string, stdout, stderr io.Writer) int {
	jsonMode, path := false, ""
	for _, arg := range args {
		switch {
		case arg == "--json":
			jsonMode = true
		case strings.HasPrefix(arg, "-") || path != "":
			_, _ = fmt.Fprintln(stderr, "Usage: pig piglet verify <piglet-binary> [--json]")
			return 2
		default:
			path = arg
		}
	}
	if path == "" {
		_, _ = fmt.Fprintln(stderr, "Usage: pig piglet verify <piglet-binary> [--json]")
		return 2
	}
	output := pigletVerifyOutput{Path: path}
	status, err := checkBinarySignature(path)
	output.Signed, output.Trusted, output.Signer = status.Signed, status.Trusted, status.KeyID
	output.Status = status.Describe()
	if status.Signed {
		output.Manifest = &status.Manifest
	}
	if err != nil {
		output.Error = err.Error()
		output.Status = "FAILED: " + err.Error()
	}
	output.Verified = err == nil && status.Signed
	if jsonMode {
		data, _ := json.MarshalIndent(output, "", "  ")
		_, _ = fmt.Fprintln(stdout, string(data))
	} else {
		renderVerify(stdout, output)
	}
	if !output.Verified {
		return 1
	}
	return 0
}

// checkBinarySignature verifies path's signature under the user's trust store.
func checkBinarySignature(path string) (signature.Status, error) {
	trust, err := signature.LoadTrust(signature.TrustDir())
	if err != nil {
		return signature.Status{}, fmt.Errorf("Piglet trust store: %w", err)
	}
	return signature.Check(path, signature.Policy{Trust: trust})
}

func renderVerify(stdout io.Writer, output pigletVerifyOutput) {
	_, _ = fmt.Fprintf(stdout, "Piglet Binary: %s\nSignature: %s\n", output.Path, output.Status)
	if output.Manifest == nil || output.Error != "" {
		return
	}
	m := output.Manifest
	_, _ = fmt.Fprintf(stdout, "Signed manifest:\n  Piglet: %s %s\n  target: %s\n  Pig: %s\n  Piglet definition: %s\n  resolution record: %s\n  component plan: %s\n  executable: %s (%d bytes)\n",
		m.Piglet, m.ReleaseVersion, m.Target, m.PigVersion, m.PigletDigest, m.ResolutionDigest, m.ComponentPlanDigest, m.Executable.Digest, m.Executable.Size)
	for _, component := range m.Components {
		_, _ = fmt.Fprintf(stdout, "  component %s/%s  %s  %s  %s\n", component.Kind, component.Name, component.Realization, component.Materialization, component.Digest)
	}
	for _, file := range m.Embedded {
		_, _ = fmt.Fprintf(stdout, "  embedded %s  %s\n", file.Path, file.Digest)
	}
}

// signatureSummary is the one-line signature state shown for a managed Piglet
// Binary artifact. A signed manifest must name the record that selected it.
func signatureSummary(record RecordInfo) string {
	status, err := checkBinarySignature(record.ArtifactPath)
	if err == nil && status.Signed {
		for _, check := range []struct{ name, signed, recorded string }{
			{"release version", status.Manifest.ReleaseVersion, record.ReleaseVersion},
			{"target", status.Manifest.Target, record.Target},
			{"Piglet source", status.Manifest.SourceDigest, record.PigletDigest},
			{"resolution record", status.Manifest.ResolutionDigest, record.ResolutionDigest},
			{"component plan", status.Manifest.ComponentPlanDigest, record.ComponentPlanDigest},
		} {
			if check.signed != check.recorded {
				err = fmt.Errorf("signed manifest names %s %q, managed record has %q", check.name, check.signed, check.recorded)
				break
			}
		}
	}
	if err != nil {
		return "FAILED: " + err.Error()
	}
	return status.Describe()
}
