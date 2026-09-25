package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// defaultVerifyRepo is the repository whose release workflow signs PiG's
// provenance attestations.
const defaultVerifyRepo = "MichaelKinsy/PiG"

const verifyUsage = `Usage: pig verify [--json] [--checksums FILE] [--provenance] [--repo OWNER/NAME] [--signer-workflow WORKFLOW] [--packages] [path...]

Verification starts from the SHA-256 of the bytes on disk. A version string,
file name, or download URL proves nothing. Piglet Binary signature blocks are
checked offline against the local Piglet trust and revocation policy.

  (no path)          verify this pig binary
  path               a file is verified by digest; a Piglet file (*.yaml, *.yml)
                     is validated with ` + "`pig piglet validate`" + `, and a Package or
                     extension directory with ` + "`pig install --validate-only`" + `
  --checksums FILE   require each file's digest to match its entry in a SHA256SUMS file
  --provenance       check GitHub build provenance with ` + "`gh attestation verify`" + `,
                     signed by the selected repository workflow
  --repo OWNER/NAME  repository for --provenance (default ` + defaultVerifyRepo + `)
  --signer-workflow WORKFLOW
                     expected GitHub Actions workflow (default
                     OWNER/NAME/.github/workflows/release-candidate.yml)
  --packages         verify installed Packages: npm registry signatures and
                     provenance with ` + "`npm audit signatures`" + `, and each git
                     Package's commit and clean working tree
  --json             print the report as JSON
`

type verifyCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok", "failed", or "unavailable"
	Detail string `json:"detail,omitempty"`
}

type verifyTarget struct {
	Path   string        `json:"path"`
	Kind   string        `json:"kind"`
	SHA256 string        `json:"sha256,omitempty"`
	Checks []verifyCheck `json:"checks"`
}

type verifyIdentity struct {
	PigVersion    string `json:"pigVersion"`
	Upstream      string `json:"upstreamPi"`
	Go            string `json:"go"`
	Platform      string `json:"platform"`
	Build         string `json:"build"`
	Revision      string `json:"revision,omitempty"`
	Modified      bool   `json:"modified,omitempty"`
	PigletRelease string `json:"pigletRelease,omitempty"`
}

type verifyReport struct {
	Verified bool           `json:"verified"`
	Identity verifyIdentity `json:"identity"`
	Targets  []verifyTarget `json:"targets"`
}

type verifyOptions struct {
	json           bool
	checksums      string
	provenance     bool
	repo           string
	signerWorkflow string
	packages       bool
	paths          []string
	pigletTrust    signature.Trust
}

// runVerifyCommand handles `pig verify`. It reports -1 for other commands.
func runVerifyCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "verify" {
		return -1
	}
	opts := verifyOptions{repo: defaultVerifyRepo}
	for i := 1; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--json":
			opts.json = true
		case "--provenance":
			opts.provenance = true
		case "--packages":
			opts.packages = true
		case "--checksums", "--repo", "--signer-workflow":
			if i+1 >= len(args) {
				_, _ = fmt.Fprintf(stderr, "pig verify: %s requires a value\n%s", arg, verifyUsage)
				return 2
			}
			i++
			switch arg {
			case "--checksums":
				opts.checksums = args[i]
			case "--repo":
				opts.repo = args[i]
			case "--signer-workflow":
				opts.signerWorkflow = args[i]
			}
		case "-h", "--help":
			_, _ = io.WriteString(stdout, verifyUsage)
			return 0
		default:
			if strings.HasPrefix(arg, "-") {
				_, _ = fmt.Fprintf(stderr, "pig verify: unknown option %q\n%s", arg, verifyUsage)
				return 2
			}
			opts.paths = append(opts.paths, arg)
		}
	}
	self, err := os.Executable()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig verify: locate this binary: %v\n", err)
		return 1
	}
	sums, err := readChecksums(opts.checksums)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig verify: %v\n", err)
		return 1
	}
	opts.pigletTrust, err = signature.LoadTrust(signature.TrustDir())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig verify: load Piglet trust policy: %v\n", err)
		return 1
	}
	report := verifyReport{Identity: binaryIdentity()}
	if len(opts.paths) == 0 {
		report.Targets = append(report.Targets, verifyFile(self, "binary", sums, opts))
	}
	for _, path := range opts.paths {
		report.Targets = append(report.Targets, verifyPath(self, path, sums, opts))
	}
	if opts.packages {
		report.Targets = append(report.Targets, verifyInstalledPackages()...)
	}
	report.Verified = true
	for _, target := range report.Targets {
		for _, check := range target.Checks {
			if check.Status == "failed" {
				report.Verified = false
			}
		}
	}
	if opts.json {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(report)
	} else {
		writeVerifyText(stdout, report)
	}
	if !report.Verified {
		return 1
	}
	return 0
}

func binaryIdentity() verifyIdentity {
	identity := verifyIdentity{
		PigVersion: PigVersion, Upstream: coding.UpstreamVersion, Go: runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH, Build: Build, PigletRelease: PigletBinaryVersion,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				identity.Revision = setting.Value
			case "vcs.modified":
				identity.Modified = setting.Value == "true"
			}
		}
	}
	return identity
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }() // Read-only: close cannot lose data.
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// readChecksums parses a sha256sum-format file into name -> digest.
func readChecksums(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	defer func() { _ = file.Close() }() // Read-only: close cannot lose data.
	sums := map[string]string{}
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		digest, name, ok := strings.Cut(text, " ")
		name = strings.TrimPrefix(strings.TrimLeft(name, " "), "*")
		if !ok || len(digest) != 64 || name == "" {
			return nil, fmt.Errorf("%s:%d: not a SHA-256 checksum line", path, line)
		}
		sums[filepath.Base(name)] = strings.ToLower(digest)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	return sums, nil
}

func verifyFile(path, kind string, sums map[string]string, opts verifyOptions) verifyTarget {
	target := verifyTarget{Path: path, Kind: kind}
	digest, err := fileSHA256(path)
	if err != nil {
		target.Checks = append(target.Checks, verifyCheck{Name: "digest", Status: "failed", Detail: err.Error()})
		return target
	}
	target.SHA256 = digest
	// pig additive (D18): report and enforce a Piglet Binary signature block
	// without executing the target.
	// Required-signature policy applies after a trailer identifies a Piglet
	// Binary. An unsigned generic file cannot be identified as one.
	checkTrust := opts.pigletTrust
	checkTrust.RequireSignature = false
	status, signatureErr := signature.Check(path, signature.Policy{Trust: checkTrust})
	if signatureErr == nil && status.Signed && opts.pigletTrust.RequireSignature {
		status, signatureErr = signature.Check(path, signature.Policy{Trust: opts.pigletTrust})
	}
	signatureCheck := verifyCheck{Name: "piglet signature", Detail: status.Describe()}
	switch {
	case signatureErr != nil:
		signatureCheck.Status = "failed"
		signatureCheck.Detail = signatureErr.Error()
	case status.Signed:
		signatureCheck.Status = "ok"
	default:
		signatureCheck.Status = "unavailable"
	}
	target.Checks = append(target.Checks, signatureCheck)
	if sums != nil {
		switch want, ok := sums[filepath.Base(path)]; {
		case !ok:
			target.Checks = append(target.Checks, verifyCheck{Name: "checksum", Status: "failed", Detail: filepath.Base(path) + " is not listed in " + opts.checksums})
		case want != digest:
			target.Checks = append(target.Checks, verifyCheck{Name: "checksum", Status: "failed", Detail: "digest differs from " + opts.checksums + " (" + want + ")"})
		default:
			target.Checks = append(target.Checks, verifyCheck{Name: "checksum", Status: "ok", Detail: "matches " + opts.checksums})
		}
	}
	if opts.provenance {
		target.Checks = append(target.Checks, verifyProvenance(path, opts.repo, opts.signerWorkflow))
	}
	return target
}

// verifyProvenance checks the GitHub build-provenance attestation with the
// gh CLI, which verifies the Sigstore bundle: certificate chain, transparency
// log inclusion, and a signer that must be the repository's release workflow.
func verifyProvenance(path, repo, signerWorkflow string) verifyCheck {
	if signerWorkflow == "" {
		signerWorkflow = repo + "/.github/workflows/release-candidate.yml"
	}
	args := []string{"attestation", "verify", path, "--repo", repo,
		"--signer-workflow", signerWorkflow}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return verifyCheck{Name: "provenance", Status: "unavailable", Detail: "install the GitHub CLI (https://cli.github.com), then run: gh " + strings.Join(args, " ")}
	}
	output, err := exec.Command(gh, args...).CombinedOutput()
	if err != nil {
		return verifyCheck{Name: "provenance", Status: "failed", Detail: strings.TrimSpace(string(output))}
	}
	return verifyCheck{Name: "provenance", Status: "ok", Detail: "signed by " + signerWorkflow}
}

func verifyPath(self, path string, sums map[string]string, opts verifyOptions) verifyTarget {
	info, err := os.Stat(path)
	if err != nil {
		return verifyTarget{Path: path, Kind: "missing", Checks: []verifyCheck{{Name: "exists", Status: "failed", Detail: err.Error()}}}
	}
	switch ext := strings.ToLower(filepath.Ext(path)); {
	case info.IsDir():
		return delegate(self, path, "resource", "install", "--validate-only", path)
	case ext == ".yaml" || ext == ".yml":
		return delegate(self, path, "piglet", "piglet", "validate", path)
	default:
		return verifyFile(path, "file", sums, opts)
	}
}

// delegate runs the owning structural validator in a child process so it
// keeps its own flags, output, and exit status.
func delegate(self, path, kind string, command ...string) verifyTarget {
	var output bytes.Buffer
	cmd := exec.Command(self, command...)
	cmd.Stdout, cmd.Stderr = &output, &output
	check := verifyCheck{Name: "pig " + strings.Join(command[:len(command)-1], " "), Status: "ok", Detail: strings.TrimSpace(output.String())}
	if err := cmd.Run(); err != nil {
		check.Status = "failed"
		check.Detail = strings.TrimSpace(output.String())
		if _, ok := errors.AsType[*exec.ExitError](err); !ok {
			check.Detail = err.Error()
		}
	}
	return verifyTarget{Path: path, Kind: kind, Checks: []verifyCheck{check}}
}

// verifyInstalledPackages checks user-scope npm and git Packages.
func verifyInstalledPackages() []verifyTarget {
	agentDir := codingagent.AgentDir()
	var targets []verifyTarget
	npmRoot := codingagent.NPMInstallRoot("", agentDir, false)
	if _, err := os.Stat(filepath.Join(npmRoot, "package.json")); err == nil {
		target := verifyTarget{Path: npmRoot, Kind: "npm packages"}
		npm, lookErr := exec.LookPath("npm")
		if lookErr != nil {
			target.Checks = append(target.Checks, verifyCheck{Name: "registry signatures", Status: "unavailable", Detail: "npm is not on PATH"})
		} else {
			cmd := exec.Command(npm, "audit", "signatures")
			cmd.Dir = npmRoot
			output, err := cmd.CombinedOutput()
			check := verifyCheck{Name: "registry signatures", Status: "ok", Detail: strings.TrimSpace(string(output))}
			if err != nil {
				check.Status = "failed"
			}
			target.Checks = append(target.Checks, check)
		}
		targets = append(targets, target)
	}
	gitRoot := codingagent.GitInstallRoot("", agentDir, false)
	_ = filepath.WalkDir(gitRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.IsDir() || entry.Name() != ".git" {
			return nil
		}
		repo := filepath.Dir(path)
		target := verifyTarget{Path: repo, Kind: "git package"}
		head, headErr := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		status, statusErr := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=no").Output()
		switch {
		case headErr != nil || statusErr != nil:
			target.Checks = append(target.Checks, verifyCheck{Name: "commit", Status: "failed", Detail: "git cannot read this Package"})
		case len(bytes.TrimSpace(status)) > 0:
			target.Checks = append(target.Checks, verifyCheck{Name: "commit", Status: "failed", Detail: "tracked files differ from commit " + strings.TrimSpace(string(head))})
		default:
			target.Checks = append(target.Checks, verifyCheck{Name: "commit", Status: "ok", Detail: strings.TrimSpace(string(head)) + ", clean"})
		}
		targets = append(targets, target)
		return filepath.SkipDir
	})
	return targets
}

func writeVerifyText(stdout io.Writer, report verifyReport) {
	id := report.Identity
	_, _ = fmt.Fprintf(stdout, "pig %s+%s %s %s %s, build %s\n", id.PigVersion, id.Upstream, id.Go, id.Platform, id.Revision, id.Build)
	for _, target := range report.Targets {
		_, _ = fmt.Fprintf(stdout, "\n%s (%s)\n", target.Path, target.Kind)
		if target.SHA256 != "" {
			_, _ = fmt.Fprintf(stdout, "  sha256:%s\n", target.SHA256)
		}
		for _, check := range target.Checks {
			mark := map[string]string{"ok": "ok  ", "failed": "FAIL", "unavailable": "n/a "}[check.Status]
			_, _ = fmt.Fprintf(stdout, "  %s %s", mark, check.Name)
			if check.Detail != "" {
				_, _ = fmt.Fprintf(stdout, ": %s", strings.ReplaceAll(check.Detail, "\n", "\n       "))
			}
			_, _ = io.WriteString(stdout, "\n")
		}
	}
	if len(report.Targets) == 1 && report.Targets[0].Kind == "binary" && len(report.Targets[0].Checks) == 0 {
		_, _ = io.WriteString(stdout, "\nCompare the digest with the release SHA256SUMS (--checksums), and check provenance with --provenance.\n")
	}
}
