package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var exactDependencyVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// dependencyLockfile is the mirror's root lockfile. It pins a dependency that no package manifest declares, such as a transitive SDK package Pi's build bundles.
const dependencyLockfile = "package-lock.json"

// dependencyManifest reports a manifest a dependency proof may cite: a workspace package.json, or the root lockfile.
func dependencyManifest(manifest string) bool {
	return manifest == dependencyLockfile || strings.HasPrefix(manifest, "packages/") && path.Clean(manifest) == manifest && path.Base(manifest) == "package.json"
}

type dependencySource struct {
	Reference string `json:"reference"`
	Manifest  string `json:"manifest"`
	Package   string `json:"package"`
	Version   string `json:"version"`
	Source    string `json:"source"`
	SHA256    string `json:"sha256"`
	Integrity string `json:"integrity"`
}

// loadDependencySources admits only reviewed snapshots tied to an exact dependency pin in the current mirror. The CI path is offline; snapshot capture verifies npm archive integrity before recording source digests.
func (u *upstreamIndex) loadDependencySources(root string) (returnErr error) {
	file, err := os.Open(filepath.Join(root, "test/parity", "dependency-sources.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var records []dependencySource
	if err := decoder.Decode(&records); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("dependency source ledger has trailing data")
	}
	bounded, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, bounded.Close()) }()
	u.dependencies = map[string]string{}
	for _, record := range records {
		prefix := "node_modules/" + record.Package + "/"
		if record.Package == "" || !exactDependencyVersion.MatchString(record.Version) || !strings.HasPrefix(record.Reference, prefix) || path.Clean(record.Reference) != record.Reference || !strings.HasPrefix(record.Source, "test/parity/dependency-sources/") || path.Clean(record.Source) != record.Source || !dependencyManifest(record.Manifest) {
			return fmt.Errorf("invalid dependency source record: %s", record.Reference)
		}
		if _, exists := u.dependencies[record.Reference]; exists {
			return fmt.Errorf("duplicate dependency source: %s", record.Reference)
		}
		integrity, ok := strings.CutPrefix(record.Integrity, "sha512-")
		decoded, decodeErr := base64.StdEncoding.DecodeString(integrity)
		if !ok || decodeErr != nil || len(decoded) != 64 {
			return fmt.Errorf("invalid package integrity: %s", record.Reference)
		}
		manifest, err := u.content(record.Manifest)
		if err != nil {
			return err
		}
		var declaration struct {
			Dependencies map[string]string `json:"dependencies"`
			Packages     map[string]struct {
				Version   string `json:"version"`
				Integrity string `json:"integrity"`
			} `json:"packages"`
		}
		if err := json.Unmarshal([]byte(manifest), &declaration); err != nil {
			return err
		}
		pinned := declaration.Dependencies[record.Package]
		if record.Manifest == dependencyLockfile {
			// A transitive dependency is pinned by the lockfile entry Pi's build resolved, which also fixes the archive integrity.
			locked := declaration.Packages["node_modules/"+record.Package]
			if locked.Integrity != record.Integrity {
				return fmt.Errorf("dependency proof for %s requires integrity %s in %s; found %q", record.Package, record.Integrity, record.Manifest, locked.Integrity)
			}
			pinned = locked.Version
		}
		if pinned != record.Version {
			return fmt.Errorf("dependency proof for %s requires exact %s pin in %s; found %q", record.Package, record.Version, record.Manifest, pinned)
		}
		text, err := bounded.ReadFile(filepath.FromSlash(record.Source))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(text)
		if hex.EncodeToString(digest[:]) != record.SHA256 {
			return fmt.Errorf("dependency source digest mismatch: %s", record.Reference)
		}
		u.dependencies[record.Reference] = string(text)
	}
	return nil
}
