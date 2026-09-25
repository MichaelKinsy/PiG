package piglet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
)

// pig additive (D18): Remote Piglet origins bind installed source bytes to
// their npm or Git resolution without adding the source to Package settings.
// pigletOrigin records the remote source resolution that produced an installed
// Piglet source. The record lives beside the installed YAML.
type pigletOrigin struct {
	Source          string `json:"source"`
	ResolvedVersion string `json:"resolvedVersion"`
	Integrity       string `json:"integrity,omitempty"`
	Commit          string `json:"commit,omitempty"`
	PigletDigest    string `json:"pigletDigest"`
	AddedAt         string `json:"addedAt"`
}

func newPigletOrigin(source, materializedRoot string, pigletData []byte, now time.Time) (pigletOrigin, error) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil {
		return pigletOrigin{}, fmt.Errorf("Piglet origin source: %w", err)
	}
	resolvedVersion, integrity, commit, err := inspectPigletOrigin(ref, materializedRoot)
	if err != nil {
		return pigletOrigin{}, err
	}
	origin := pigletOrigin{
		Source:          source,
		ResolvedVersion: resolvedVersion,
		Integrity:       integrity,
		Commit:          commit,
		PigletDigest:    digestPigletData(pigletData),
		AddedAt:         now.UTC().Format(time.RFC3339Nano),
	}
	if err := origin.validate(); err != nil {
		return pigletOrigin{}, err
	}
	return origin, nil
}

func inspectPigletOrigin(ref sourceref.Ref, materializedRoot string) (string, string, string, error) {
	switch ref.Kind {
	case sourceref.KindNPM:
		data, err := os.ReadFile(filepath.Join(materializedRoot, "package.json"))
		if err != nil {
			return "", "", "", fmt.Errorf("read materialized npm package: %w", err)
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return "", "", "", fmt.Errorf("read materialized npm package: %w", err)
		}
		return manifest.Version, readNPMOriginIntegrity(materializedRoot), "", nil
	case sourceref.KindGit:
		command := exec.Command("git", "-C", materializedRoot, "rev-parse", "HEAD")
		output, err := command.CombinedOutput()
		if err != nil {
			return "", "", "", fmt.Errorf("resolve materialized Git commit: %w: %s", err, strings.TrimSpace(string(output)))
		}
		commit := strings.TrimSpace(string(output))
		resolvedVersion := ref.GitRef
		if resolvedVersion == "" {
			resolvedVersion = commit
		}
		return resolvedVersion, "", commit, nil
	default:
		return "", "", "", fmt.Errorf("Piglet origin source %q must use npm or Git", ref.Raw)
	}
}

func readNPMOriginIntegrity(materializedRoot string) string {
	nodeModules := filepath.Clean(materializedRoot)
	for filepath.Base(nodeModules) != "node_modules" {
		parent := filepath.Dir(nodeModules)
		if parent == nodeModules {
			return ""
		}
		nodeModules = parent
	}
	installRoot := filepath.Dir(nodeModules)
	relative, err := filepath.Rel(installRoot, materializedRoot)
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(installRoot, "package-lock.json"))
	if err != nil {
		return ""
	}
	var lock struct {
		Packages map[string]struct {
			Integrity string `json:"integrity"`
		} `json:"packages"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return ""
	}
	return lock.Packages[filepath.ToSlash(relative)].Integrity
}

// validateRemotePigletOriginSource keeps authentication material out of origin
// records because list --json returns their source to callers.
func validateRemotePigletOriginSource(ref sourceref.Ref) error {
	if ref.Kind == sourceref.KindNPM {
		return nil
	}
	if ref.Kind != sourceref.KindGit {
		return fmt.Errorf("Piglet origin source must use npm or Git")
	}
	if !strings.Contains(ref.GitRepo, "://") {
		if strings.Contains(ref.GitRepo, "?") {
			return fmt.Errorf("Git Piglet source URLs must not include query parameters")
		}
		return nil
	}
	parsed, err := url.Parse(ref.GitRepo)
	if err != nil {
		return fmt.Errorf("Git Piglet source URL is invalid")
	}
	var hasPassword bool
	if parsed.User != nil {
		_, hasPassword = parsed.User.Password()
	}
	if hasPassword || (parsed.User != nil && parsed.Scheme != "ssh") {
		return fmt.Errorf("Git Piglet source URLs must not include credentials; configure a Git credential helper or use SSH")
	}
	if parsed.RawQuery != "" {
		return fmt.Errorf("Git Piglet source URLs must not include query parameters")
	}
	return nil
}

func (o pigletOrigin) validate() error {
	ref, err := sourceref.Parse(o.Source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil {
		return fmt.Errorf("Piglet origin source: %w", err)
	}
	if err := validateRemotePigletOriginSource(ref); err != nil {
		return err
	}
	if o.ResolvedVersion == "" {
		return fmt.Errorf("Piglet origin resolvedVersion is required")
	}
	switch ref.Kind {
	case sourceref.KindNPM:
		if o.Integrity == "" {
			return fmt.Errorf("npm Piglet origin integrity is required")
		}
		if o.Commit != "" {
			return fmt.Errorf("npm Piglet origin cannot include a Git commit")
		}
	case sourceref.KindGit:
		if !validGitCommit(o.Commit) {
			return fmt.Errorf("Git Piglet origin commit must be a lowercase hexadecimal object ID")
		}
		if o.Integrity != "" {
			return fmt.Errorf("Git Piglet origin cannot include npm integrity")
		}
	default:
		return fmt.Errorf("Piglet origin source %q must use npm or Git", o.Source)
	}
	if !validSHA256Digest(o.PigletDigest) {
		return fmt.Errorf("Piglet origin pigletDigest must be sha256:<64 lowercase hex characters>")
	}
	if _, err := time.Parse(time.RFC3339Nano, o.AddedAt); err != nil {
		return fmt.Errorf("Piglet origin addedAt: %w", err)
	}
	return nil
}

func validGitCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	if strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validSHA256Digest(value string) bool {
	raw, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(raw) != sha256.Size*2 || strings.ToLower(raw) != raw {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func digestPigletData(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func originPathForPiglet(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + ".origin.json"
}

func marshalPigletOrigin(origin pigletOrigin) ([]byte, error) {
	data, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readPigletOrigin(path string) (*pigletOrigin, error) {
	originPath := originPathForPiglet(path)
	data, err := os.ReadFile(originPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Piglet origin %s: %w", originPath, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var origin pigletOrigin
	if err := decoder.Decode(&origin); err != nil {
		return nil, fmt.Errorf("Piglet origin %s: %w", originPath, err)
	}
	if err := rejectTrailingOriginJSON(decoder); err != nil {
		return nil, fmt.Errorf("Piglet origin %s: %w", originPath, err)
	}
	if err := origin.validate(); err != nil {
		return nil, fmt.Errorf("Piglet origin %s: %w", originPath, err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Piglet source %s: %w", path, err)
	}
	if digest := digestPigletData(data); origin.PigletDigest != digest {
		return nil, fmt.Errorf("Piglet origin %s digest %s does not match source digest %s", originPath, origin.PigletDigest, digest)
	}
	return &origin, nil
}

func rejectTrailingOriginJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return fmt.Errorf("multiple JSON values are not allowed")
}
