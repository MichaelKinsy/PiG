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
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var exactDependencyVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// dependencyLockfile is the mirror's root lockfile. It pins a dependency that no package manifest declares, such as a transitive SDK package Pi's build bundles.
const dependencyLockfile = "package-lock.json"

// nodeRuntimePrefix starts the reference of a source file Node ships in its own tree (deps/minimatch behind fs.globSync), which no npm package pins. It cannot be mistaken for a node_modules reference.
const nodeRuntimePrefix = "node-deps/"

// nodeVersionFile pins the Node release PiG's parity oracle runs; a record for Node's own source cites it as its manifest.
const nodeVersionFile = ".node-version"

// dependencyManifest reports a manifest a dependency proof may cite: a workspace package.json, or the root lockfile.
func dependencyManifest(manifest string) bool {
	return manifest == dependencyLockfile || strings.HasPrefix(manifest, "packages/") && path.Clean(manifest) == manifest && path.Base(manifest) == "package.json"
}

// backportsScript is the generator of the Pi fixes the release line carries beyond the pin; its dependency entries move a dependency a Pi package pins exactly.
const backportsScript = "automation/gen/pi-post-release-backports.mjs"

// carriedDependency is a dependency entry of backportsScript: Pi package `Package` (pi-coding-agent) pins `Name` at `From` in the mirror and at `To` from commit `ID` on.
type carriedDependency struct {
	ID        string `json:"id"`
	Package   string `json:"package"`
	Name      string `json:"name"`
	From      string `json:"from"`
	To        string `json:"to"`
	Integrity string `json:"integrity"`
}

// loadCarriedDependencies reads the dependency entries of the root's backports script; a root without the script carries none.
func loadCarriedDependencies(root string) ([]carriedDependency, error) {
	script := filepath.Join(root, filepath.FromSlash(backportsScript))
	if _, err := os.Stat(script); os.IsNotExist(err) {
		return nil, nil
	}
	//nolint:gosec // G204 (class a): program is literal node; the arguments are the repository's constant backports script path and the fixed --dependencies flag.
	out, err := exec.Command("node", script, "--dependencies").Output()
	if err != nil {
		return nil, fmt.Errorf("list the dependencies %s carries: %w", backportsScript, err)
	}
	var carried []carriedDependency
	if err := json.Unmarshal(out, &carried); err != nil {
		return nil, fmt.Errorf("parse the dependencies %s carries: %w", backportsScript, err)
	}
	return carried, nil
}

// carriedPin returns the carried entry that moves the pin of record's package in the manifest it cites, when the mirror still pins the version the entry replaces.
func carriedPin(carried []carriedDependency, record dependencySource, mirrorPin string) (carriedDependency, bool) {
	dir, ok := strings.CutPrefix(record.Manifest, "packages/")
	if !ok {
		return carriedDependency{}, false
	}
	owner := "pi-" + path.Dir(dir)
	for _, entry := range carried {
		if entry.Package == owner && entry.Name == record.Package && entry.From == mirrorPin {
			return entry, true
		}
	}
	return carriedDependency{}, false
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
	carried, err := loadCarriedDependencies(root)
	if err != nil {
		return err
	}
	u.dependencies = map[string]string{}
	for _, record := range records {
		prefix := "node_modules/" + record.Package + "/"
		nodeRuntime := record.Package == "node" && record.Manifest == nodeVersionFile
		if nodeRuntime {
			prefix = nodeRuntimePrefix
		}
		if record.Package == "" || !exactDependencyVersion.MatchString(record.Version) || !strings.HasPrefix(record.Reference, prefix) || path.Clean(record.Reference) != record.Reference || !strings.HasPrefix(record.Source, "test/parity/dependency-sources/") || path.Clean(record.Source) != record.Source || (!nodeRuntime && !dependencyManifest(record.Manifest)) {
			return fmt.Errorf("invalid dependency source record: %s", record.Reference)
		}
		if _, exists := u.dependencies[record.Reference]; exists {
			return fmt.Errorf("duplicate dependency source: %s", record.Reference)
		}
		if nodeRuntime {
			// Node's source is verified against the digest of the release archive it shipped in (nodejs.org SHASUMS256.txt), and the record must name the Node release PiG pins.
			archive, ok := strings.CutPrefix(record.Integrity, "sha256-")
			decoded, decodeErr := hex.DecodeString(archive)
			if !ok || decodeErr != nil || len(decoded) != sha256.Size {
				return fmt.Errorf("invalid Node release archive digest: %s", record.Reference)
			}
			pin, err := bounded.ReadFile(nodeVersionFile)
			if err != nil {
				return err
			}
			if pinned := strings.TrimSpace(string(pin)); pinned != record.Version {
				return fmt.Errorf("dependency proof for Node source requires exact %s pin in %s; found %q", record.Version, nodeVersionFile, pinned)
			}
		} else if err := u.verifyNpmPin(record, carried); err != nil {
			return err
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

// verifyNpmPin checks an npm record's archive integrity and its exact pin in the cited manifest or the lockfile entry. The pin in force is the mirror's, or, when
// a dependency entry of the backports script replaces it, the entry's version, whose archive integrity the record must also carry.
func (u *upstreamIndex) verifyNpmPin(record dependencySource, carried []carriedDependency) error {
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
	} else if entry, ok := carriedPin(carried, record, pinned); ok {
		if entry.Integrity != record.Integrity {
			return fmt.Errorf("dependency proof for %s requires integrity %s carried for Pi %s; found %q", record.Package, entry.Integrity, entry.ID, record.Integrity)
		}
		pinned = entry.To
	}
	if pinned != record.Version {
		return fmt.Errorf("dependency proof for %s requires exact %s pin in %s; found %q", record.Package, record.Version, record.Manifest, pinned)
	}
	return nil
}
