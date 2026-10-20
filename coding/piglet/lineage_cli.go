package piglet

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

const lineageUsage = "Usage: pig piglet lineage <name|path|binary> [--source <name|path>] [--workspace <dir>] [--json]"

// BinaryLineage is the lineage a signed Piglet Binary carries: the identity it
// records, not an extends chain (the record keeps digests, not the chain).
// pig additive (D170): `pig piglet lineage` reports a Binary from its signed manifest.
type BinaryLineage struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest"`
	Source  string `json:"sourceDigest"`
	Signer  string `json:"signer"`
	Trusted bool   `json:"trusted"`
	// Verified is false when the signature check fails: the executable bytes changed after signing, the signer is revoked, or the trust policy rejects the signer. The identity above is then only what the unverified manifest claims.
	Verified  bool   `json:"verified"`
	Signature string `json:"signature"`
}

// LineageOutput is the `pig piglet lineage` result. Chain is root first.
// Advisories lists advisories the chain inherits; stock Pig records none, so
// the list stays empty until a Piglet source supplies them.
type LineageOutput struct {
	Kind       string           `json:"kind"`
	Piglet     string           `json:"piglet"`
	Chain      []LineageLink    `json:"chain"`
	Binary     *BinaryLineage   `json:"binary,omitempty"`
	Provenance *ProvenanceCheck `json:"provenance,omitempty"`
	Advisories []string         `json:"advisories"`
}

func cmdLineage(args []string, stdout, stderr io.Writer) int {
	jsonMode, target, source, workspace := false, "", "", ""
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--json":
			jsonMode = true
		case arg == "--source" && i+1 < len(args) && source == "":
			i++
			source = args[i]
		case arg == "--workspace" && i+1 < len(args) && workspace == "":
			i++
			workspace = args[i]
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprintln(stdout, lineageUsage)
			return 0
		case strings.HasPrefix(arg, "-") || target != "":
			_, _ = fmt.Fprintln(stderr, lineageUsage)
			return 2
		default:
			target = arg
		}
	}
	if target == "" {
		_, _ = fmt.Fprintln(stderr, lineageUsage)
		return 2
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	out, err := Lineage(target, source, workspace)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if jsonMode {
		//portlint:allow jsonescape PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
		data, _ := json.MarshalIndent(out, "", "  ")
		_, _ = fmt.Fprintln(stdout, string(data))
	} else {
		RenderLineage(stdout, out)
	}
	if out.Provenance != nil && !out.Provenance.Matches || out.Binary != nil && !out.Binary.Verified {
		return 1
	}
	return 0
}

// Lineage reports target, a Piglet name or source path or a signed Piglet
// Binary. For a Binary, source (a name or path) supplies the extends chain and
// the Binary's recorded digests are checked against it. It reads local files.
func Lineage(target, source, workspace string) (*LineageOutput, error) {
	info, statErr := os.Stat(target)
	isFile := statErr == nil && info.Mode().IsRegular()
	var status signature.Status
	var err error
	if isFile {
		status, err = checkBinarySignature(target)
	}
	if !status.Signed {
		path, resolveErr := Resolve(target)
		if resolveErr != nil {
			if isFile {
				return nil, fmt.Errorf("%s is neither a Piglet source nor a signed Piglet Binary (%w)", target, resolveErr)
			}
			return nil, resolveErr
		}
		report, err := SourceLineage(path, workspace)
		if err != nil {
			return nil, err
		}
		return &LineageOutput{Kind: "source", Piglet: report.Piglet, Chain: report.Chain, Advisories: []string{}}, nil
	}
	// A signed Binary whose check fails is still reported, with the failure, so its claimed identity is never shown as verified.
	signatureStatus := status.Describe()
	if err != nil {
		signatureStatus = "FAILED: " + err.Error()
	}
	m := status.Manifest
	out := &LineageOutput{Kind: "binary", Piglet: m.Piglet, Advisories: []string{}, Binary: &BinaryLineage{
		Path: target, Name: m.Piglet, Version: m.ReleaseVersion, Digest: m.PigletDigest, Source: m.SourceDigest, Signer: status.KeyID, Trusted: status.Trusted,
		Verified: err == nil, Signature: signatureStatus,
	}}
	if source != "" {
		path, err := Resolve(source)
		if err != nil {
			return nil, err
		}
		report, err := SourceLineage(path, workspace)
		if err != nil {
			return nil, err
		}
		out.Chain = report.Chain
		check, _ := CheckProvenance(m, source, workspace)
		out.Provenance = check
	}
	return out, nil
}

// ProvenanceCheck compares the Piglet digests a Binary's signed manifest
// records with those recomputed from a source Piglet. PigletDigest is the
// digest of the Piglet definition the source bakes into a Binary.
type ProvenanceCheck struct {
	Source       string `json:"source"`
	SourceDigest string `json:"sourceDigest"`
	PigletDigest string `json:"pigletDigest"`
	Matches      bool   `json:"matches"`
	//portlint:allow emptydrop PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
	Mismatches []string `json:"mismatches,omitempty"`
}

// CheckProvenance recomputes the source digest of the Piglet named by source
// and the digest of the Piglet definition it bakes into a Binary, as the build
// does, and compares them with manifest. workspace anchors workspace: paths as
// the build's --workspace did (the build defaults to its working directory).
// A mismatch returns the check and an error naming each differing digest.
func CheckProvenance(manifest signature.Manifest, source, workspace string) (*ProvenanceCheck, error) {
	path, err := Resolve(source)
	if err != nil {
		return nil, err
	}
	resolution, err := ResolveEffectiveWithOptions(path, ResolveOptions{Workspace: workspace})
	if err != nil {
		return nil, fmt.Errorf("resolve effective Piglet %s: %w", path, err)
	}
	baked, err := Bake(resolution.Piglet, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("bake Piglet %s: %w", path, err)
	}
	check := &ProvenanceCheck{Source: path, SourceDigest: resolution.SourceDigest, PigletDigest: digestPigletBytes(baked)}
	if manifest.SourceDigest != check.SourceDigest {
		check.Mismatches = append(check.Mismatches, fmt.Sprintf("source digest: the Binary records %s, %s is %s", manifest.SourceDigest, path, check.SourceDigest))
	}
	if manifest.PigletDigest != check.PigletDigest {
		check.Mismatches = append(check.Mismatches, fmt.Sprintf("Piglet digest: the Binary records %s, %s resolves to %s", manifest.PigletDigest, path, check.PigletDigest))
	}
	check.Matches = len(check.Mismatches) == 0
	if !check.Matches {
		return check, fmt.Errorf("provenance mismatch: %s", strings.Join(check.Mismatches, "; "))
	}
	return check, nil
}

func renderProvenance(w io.Writer, check *ProvenanceCheck) {
	if check == nil {
		return
	}
	if check.Matches {
		_, _ = fmt.Fprintf(w, "Provenance: matches %s\n", check.Source)
		return
	}
	_, _ = fmt.Fprintf(w, "Provenance: MISMATCH against %s\n", check.Source)
	for _, mismatch := range check.Mismatches {
		_, _ = fmt.Fprintf(w, "  %s\n", mismatch)
	}
}

// RenderLineage writes the chain root first. Each link below the root lists
// what it adds (+), removes (-) and alters (~) against its base.
func RenderLineage(w io.Writer, out *LineageOutput) {
	_, _ = fmt.Fprintf(w, "Piglet lineage: %s (%s)\n", out.Piglet, out.Kind)
	if b := out.Binary; b != nil {
		trust := "not in your trust store"
		if b.Trusted {
			trust = "trusted"
		}
		_, _ = fmt.Fprintf(w, "Binary: %s\n  signature: %s\n  name: %s\n  version: %s\n  Piglet digest: %s\n  source digest: %s\n  signer: %s (%s)\n", b.Path, b.Signature, b.Name, orNone(b.Version), b.Digest, b.Source, b.Signer, trust)
		if len(out.Chain) == 0 {
			_, _ = fmt.Fprintln(w, "Chain: not recorded in a Binary; pass --source <name|path> to see it")
		}
	}
	renderProvenance(w, out.Provenance)
	for i, link := range out.Chain {
		prefix := "  "
		if i > 0 {
			prefix = "  " + strings.Repeat("   ", i-1) + "└─ "
		}
		_, _ = fmt.Fprintf(w, "%s%s %s  %s  %s\n", prefix, link.Name, orNone(link.Version), link.Digest, link.Source)
		if link.Changes == nil {
			continue
		}
		pad := strings.Repeat(" ", 3*i+4)
		if link.Changes.Empty() {
			_, _ = fmt.Fprintf(w, "%s(no change against its base)\n", pad)
			continue
		}
		for _, row := range []struct {
			name string
			set  SetChange
		}{{"extensions", link.Changes.Extensions}, {"packages", link.Changes.Packages}, {"skills", link.Changes.Skills}, {"tools", link.Changes.Tools}, {"permissions", link.Changes.Permissions}, {"slots", link.Changes.Slots}, {"strip", link.Changes.Strip}, {"settings", link.Changes.Settings}} {
			for _, part := range []struct {
				mark, label string
				names       []string
			}{{"+", "added", row.set.Added}, {"-", "removed", row.set.Removed}, {"~", "changed", row.set.Changed}} {
				if len(part.names) > 0 {
					_, _ = fmt.Fprintf(w, "%s%s %s %s: %s\n", pad, part.mark, row.name, part.label, strings.Join(part.names, ", "))
				}
			}
		}
	}
	if len(out.Advisories) == 0 {
		_, _ = fmt.Fprintln(w, "Advisories: none recorded")
		return
	}
	_, _ = fmt.Fprintln(w, "Advisories:")
	for _, advisory := range out.Advisories {
		_, _ = fmt.Fprintf(w, "  %s\n", advisory)
	}
}

func orNone(v string) string {
	if v == "" {
		return "(no version)"
	}
	return v
}
