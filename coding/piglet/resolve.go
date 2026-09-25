package piglet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Resolve finds a piglet by name, checking the resolution order:
//
//  1. Explicit path (already absolute or relative)
//  2. PIG_PIGLET_PATH env var
//  3. PIG_PIGLET_NAME env var → search paths
//  4. Workspace: .pig/piglets/<name>.yaml
//  5. User: ~/.pig/piglets/<name>.yaml
//  6. System: /etc/pig/piglets/<name>.yaml
func Resolve(nameOrPath string) (string, error) {
	// 1. Explicit path: if it looks like a path, use it directly
	if nameOrPath != "" && (strings.Contains(nameOrPath, "/") || strings.HasSuffix(nameOrPath, ".yaml") || strings.HasSuffix(nameOrPath, ".yml")) {
		if _, err := os.Stat(nameOrPath); err != nil {
			return "", fmt.Errorf("piglet path %s: %w", nameOrPath, err)
		}
		return nameOrPath, nil
	}

	// 2. PIG_PIGLET_PATH: explicit path from env
	if envPath := os.Getenv("PIG_PIGLET_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err != nil {
			return "", fmt.Errorf("PIG_PIGLET_PATH=%s: %w", envPath, err)
		}
		return envPath, nil
	}

	// Determine the piglet name
	name := nameOrPath
	if name == "" {
		name = os.Getenv("PIG_PIGLET_NAME")
	}
	if name == "" {
		return "", fmt.Errorf("no piglet specified")
	}

	// 3-6. Search paths
	candidates := searchPaths(name)
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("piglet %q not found in search paths:\n  %s",
		name, strings.Join(candidates, "\n  "))
}

// searchPaths returns the ordered list of candidate paths for a piglet name.
func searchPaths(name string) []string {
	var paths []string
	filename := pigletFilename(name)

	// Workspace: .pig/piglets/<name>.yaml
	cwd, err := os.Getwd()
	if err == nil {
		paths = append(paths, filepath.Join(codingagent.ProjectConfigDir(cwd), "piglets", filename))
	}

	// User Piglet source (honors PIG_HOME), matching `pig piglet add`.
	paths = append(paths, filepath.Join(codingagent.PigletsDir(), filename))

	// System: /etc/pig/piglets/<name>.yaml
	paths = append(paths, filepath.Join("/etc", "pig", "piglets", filename))

	return paths
}

func pigletFilename(name string) string {
	if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
		return name
	}
	return name + ".yaml"
}

// RecordInfo describes one managed Piglet Binary record/artifact facet.
type RecordInfo struct {
	Path                string
	ResolutionPath      string
	ArtifactPath        string
	PigletDigest        string
	ReleaseVersion      string
	Target              string
	ArtifactDigest      string
	VerificationOK      bool
	ComponentPlanDigest string
	ResolutionDigest    string
	BinaryDigest        string
	CurrentPath         string
	Components          []RecordComponent
}

// RecordComponent is one executable component's realization and materialization
// as recorded in a managed Piglet resolution record's component plan.
type RecordComponent struct {
	Kind            string
	Name            string
	Realization     string
	Materialization string
}

// PigletInfo describes discovered Piglet source and Binary facets.
type PigletInfo struct {
	Name        string
	Description string
	Path        string
	Location    string // "workspace", "user", "system", or "binary"
	Records     []RecordInfo
}

// List returns all discoverable Piglet rows, merging source and managed
// Piglet Binary records. Malformed state is an error.
// pig additive (D41): Piglet inventory includes Piglet Binary facets.
func List() ([]PigletInfo, error) {
	piglets := make([]PigletInfo, 0)
	var errs []error
	for _, dir := range allPigletDirs() {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("read Piglet directory %s: %w", dir, err))
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if filepath.Ext(name) != ".yaml" && filepath.Ext(name) != ".yml" {
				continue
			}
			path := filepath.Join(dir, name)
			parsed, err := Parse(path)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			piglets = append(piglets, PigletInfo{
				Name: parsed.Name, Description: parsed.Description, Path: path, Location: classifyLocation(dir),
			})
		}
	}
	records, recordErrs := listManagedRecords()
	errs = append(errs, recordErrs...)
	for _, record := range records {
		matched := false
		for i := range piglets {
			if piglets[i].Name != record.Piglet || piglets[i].Path == "" {
				continue
			}
			digest, err := pigletSourceDigest(piglets[i].Path)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if digest != record.PigletDigest {
				continue
			}
			piglets[i].Records = append(piglets[i].Records, record.RecordInfo)
			matched = true
		}
		if !matched {
			piglets = append(piglets, PigletInfo{Name: record.Piglet, Location: "binary", Records: []RecordInfo{record.RecordInfo}})
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	for i := range piglets {
		slices.SortFunc(piglets[i].Records, func(a, b RecordInfo) int {
			if value := strings.Compare(a.Target, b.Target); value != 0 {
				return value
			}
			return strings.Compare(a.ReleaseVersion, b.ReleaseVersion)
		})
	}
	slices.SortStableFunc(piglets, func(a, b PigletInfo) int {
		if value := strings.Compare(a.Name, b.Name); value != 0 {
			return value
		}
		if value := locationRank(a.Location) - locationRank(b.Location); value != 0 {
			return value
		}
		return strings.Compare(a.Path, b.Path)
	})
	return piglets, nil
}

func pigletSourceDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash Piglet source %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func locationRank(location string) int {
	switch location {
	case "workspace":
		return 0
	case "user":
		return 1
	case "installed":
		return 2
	case "system":
		return 3
	case "binary":
		return 4
	default:
		return 5
	}
}

type managedRecord struct {
	Piglet string
	RecordInfo
}

type storedRecord struct {
	Path   string
	Record artifact.Record
}

func listManagedRecords() ([]managedRecord, []error) {
	root := codingagent.PigletRecordsDir()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, findOrphanManagedArtifacts(nil)
	} else if err != nil {
		return nil, []error{fmt.Errorf("inspect Piglet record store %s: %w", root, err)}
	}
	resolutions := make(map[string]storedRecord)
	var binaries []storedRecord
	var errs []error
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, fmt.Errorf("read Piglet record path %s: %w", path, walkErr))
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			errs = append(errs, fmt.Errorf("Piglet record path %s is a symlink; managed records must be regular files", path))
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		record, err := readRecordFile(path)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		switch record.Kind {
		case artifact.RecordKindResolution:
			if err := validateManagedRecordPath(root, path, record, record); err != nil {
				errs = append(errs, err)
				return nil
			}
			if previous, exists := resolutions[record.Digest]; exists {
				errs = append(errs, fmt.Errorf("duplicate Piglet resolution record %s in %s and %s", record.Digest, previous.Path, path))
				return nil
			}
			resolutions[record.Digest] = storedRecord{Path: path, Record: record}
		case artifact.RecordKindBinary:
			binaries = append(binaries, storedRecord{Path: path, Record: record})
		default:
			errs = append(errs, fmt.Errorf("managed Piglet record %s has unsupported kind %q", path, record.Kind))
		}
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		errs = append(errs, fmt.Errorf("walk Piglet record store %s: %w", root, walkErr))
	}

	referencedArtifacts := make(map[string]struct{})
	seenBinaries := make(map[string]string)
	records := make([]managedRecord, 0, len(binaries))
	for _, stored := range binaries {
		binary := stored.Record
		if previous, exists := seenBinaries[binary.Digest]; exists {
			errs = append(errs, fmt.Errorf("duplicate Piglet Binary record %s in %s and %s", binary.Digest, previous, stored.Path))
			continue
		}
		seenBinaries[binary.Digest] = stored.Path
		if binary.Binary == nil {
			errs = append(errs, fmt.Errorf("Piglet Binary record %s has no payload", stored.Path))
			continue
		}
		resolutionStored, exists := resolutions[binary.Binary.ResolutionDigest]
		if !exists {
			errs = append(errs, fmt.Errorf("Piglet Binary record %s references missing resolution %s", stored.Path, binary.Binary.ResolutionDigest))
			continue
		}
		resolution := resolutionStored.Record
		if err := artifact.ValidateBinaryLink(resolution, binary); err != nil {
			errs = append(errs, fmt.Errorf("Piglet Binary record %s: %w", stored.Path, err))
			continue
		}
		if err := validateManagedRecordPath(root, stored.Path, binary, resolution); err != nil {
			errs = append(errs, err)
			continue
		}
		artifactPath := managedArtifactPath(resolution, binary)
		if err := validateManagedArtifact(artifactPath, binary); err != nil {
			errs = append(errs, err)
			continue
		}
		referencedArtifacts[artifactPath] = struct{}{}
		records = append(records, managedRecord{Piglet: binary.Piglet, RecordInfo: RecordInfo{
			Path: stored.Path, ResolutionPath: resolutionStored.Path, ArtifactPath: artifactPath,
			PigletDigest: resolution.Resolution.SourceDigest, ReleaseVersion: binary.ReleaseVersion,
			Target: binary.Binary.Target, ArtifactDigest: binary.Binary.Artifact.Digest,
			VerificationOK:      binary.Binary.Verification.Passed,
			ComponentPlanDigest: resolution.Resolution.ComponentPlan.Digest,
			ResolutionDigest:    resolution.Digest, BinaryDigest: binary.Digest,
			Components: recordComponents(&resolution.Resolution.ComponentPlan),
		}})
	}
	pulled, pullErrs := pigletrelease.ListInstalled()
	errs = append(errs, pullErrs...)
	for _, item := range pulled {
		components := make([]RecordComponent, len(item.Manifest.Components))
		for i, component := range item.Manifest.Components {
			components[i] = RecordComponent{
				Kind: component.Kind, Name: component.Name,
				Realization: component.Realization, Materialization: component.Materialization,
			}
		}
		referencedArtifacts[item.ArtifactPath] = struct{}{}
		referencedArtifacts[item.CurrentPath] = struct{}{}
		records = append(records, managedRecord{Piglet: item.Index.Piglet, RecordInfo: RecordInfo{
			Path: item.ReceiptPath, ArtifactPath: item.ArtifactPath, CurrentPath: item.CurrentPath,
			PigletDigest: item.Manifest.SourceDigest, ReleaseVersion: item.Index.Version,
			Target: item.Manifest.Target, ArtifactDigest: item.Digest, VerificationOK: true,
			ComponentPlanDigest: item.Manifest.ComponentPlanDigest,
			ResolutionDigest:    item.Manifest.ResolutionDigest, BinaryDigest: item.Digest,
			Components: components,
		}})
	}
	errs = append(errs, findOrphanManagedArtifacts(referencedArtifacts)...)
	return records, errs
}

func findOrphanManagedArtifacts(referenced map[string]struct{}) []error {
	root := codingagent.PigletArtifactsDir()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return []error{fmt.Errorf("inspect artifact store %s: %w", root, err)}
	}
	var errs []error
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			errs = append(errs, fmt.Errorf("read artifact path %s: %w", path, err))
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			errs = append(errs, fmt.Errorf("managed artifact path %s is a symlink", path))
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if _, exists := referenced[path]; !exists {
			errs = append(errs, fmt.Errorf("orphan managed artifact %s has no Piglet Binary record", path))
		}
		return nil
	})
	if walkErr != nil {
		errs = append(errs, walkErr)
	}
	return errs
}

func recordComponents(plan *artifact.Plan) []RecordComponent {
	if plan == nil {
		return nil
	}
	components := make([]RecordComponent, len(plan.Components))
	for i, component := range plan.Components {
		components[i] = RecordComponent{
			Kind: string(component.Kind), Name: component.Name,
			Realization: string(component.Realization), Materialization: string(component.Materialization),
		}
	}
	return components
}

func readRecordFile(path string) (artifact.Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return artifact.Record{}, fmt.Errorf("read Piglet record %s: %w", path, err)
	}
	record, err := artifact.ParseRecord(data)
	if err != nil {
		return artifact.Record{}, fmt.Errorf("Piglet record %s: %w", path, err)
	}
	return record, nil
}

func managedArtifactPath(resolution, binary artifact.Record) string {
	version := binary.ReleaseVersion
	if version == "" {
		version = "unversioned"
	}
	return filepath.Join(
		codingagent.PigletArtifactsDir(), binary.Piglet,
		strings.TrimPrefix(resolution.Resolution.SourceDigest, "sha256:"), version,
		strings.ReplaceAll(binary.Binary.Target, "/", "-"),
		strings.TrimPrefix(binary.Binary.Artifact.Digest, "sha256:"), binary.Binary.Artifact.FileName,
	)
}

func validateManagedArtifact(path string, binary artifact.Record) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("Piglet Binary record for %q references missing managed artifact %s: %w", binary.Piglet, path, err)
	}
	expected := binary.Binary.Artifact
	if !info.Mode().IsRegular() || info.Size() != expected.Size {
		return fmt.Errorf("managed artifact %s size/type does not match Piglet Binary record", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if digest != expected.Digest {
		return fmt.Errorf("managed artifact %s digest %s does not match Piglet Binary record %s", path, digest, expected.Digest)
	}
	return nil
}

func validateManagedRecordPath(root, path string, record, resolution artifact.Record) error {
	if resolution.Resolution == nil {
		return fmt.Errorf("managed Piglet record %s has no resolution identity", path)
	}
	version := record.ReleaseVersion
	if version == "" {
		version = "unversioned"
	}
	prefix := filepath.Join(record.Piglet, strings.TrimPrefix(resolution.Resolution.SourceDigest, "sha256:"), version)
	var expected string
	switch record.Kind {
	case artifact.RecordKindResolution:
		expected = filepath.Join(prefix, string(record.Kind), strings.TrimPrefix(record.Digest, "sha256:")+".json")
	case artifact.RecordKindBinary:
		expected = filepath.Join(prefix, string(record.Kind), strings.ReplaceAll(record.Binary.Target, "/", "-"), strings.TrimPrefix(record.Digest, "sha256:")+".json")
	default:
		return fmt.Errorf("managed Piglet record %s has unsupported kind %q", path, record.Kind)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative != expected {
		return fmt.Errorf("managed Piglet record %s is stored at the wrong path; expected %s", path, filepath.Join(root, expected))
	}
	return nil
}

func allPigletDirs() []string {
	var dirs []string

	cwd, err := os.Getwd()
	if err == nil {
		dirs = append(dirs, filepath.Join(codingagent.ProjectConfigDir(cwd), "piglets"))
	}

	dirs = append(dirs, codingagent.PigletsDir())

	dirs = append(dirs, filepath.Join("/etc", "pig", "piglets"))
	return dirs
}

func classifyLocation(dir string) string {
	cwd, _ := os.Getwd()

	switch {
	case cwd != "" && strings.HasPrefix(dir, codingagent.ProjectConfigDir(cwd)):
		return "workspace"
	case dir == codingagent.PigletsDir():
		return "user"
	case strings.HasPrefix(dir, "/etc/"):
		return "system"
	default:
		return "unknown"
	}
}
