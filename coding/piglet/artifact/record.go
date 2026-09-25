package artifact

// pig additive (D18): Piglet builds emit closed, content-addressed records
// for source resolution and produced artifacts.

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// RecordKind is the closed vocabulary for first-production Piglet records.
type RecordKind string

const (
	// RecordKindResolution pins Piglet source, Resource closure, and executable planning.
	RecordKindResolution RecordKind = "piglet-resolution"
	// RecordKindBinary identifies one built Piglet Binary.
	RecordKindBinary RecordKind = "piglet-binary"
	// RecordKindImage identifies one built Piglet Image.
	RecordKindImage RecordKind = "piglet-image"
)

// InputPin is one exact Resource or dependency identity in a Piglet resolution.
type InputPin struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest"`
	Package string `json:"package,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
}

// ResolutionInput contains source-resolution facts before derived digests are calculated.
type ResolutionInput struct {
	SourceDigest    string
	EffectiveDigest string
	Inputs          []InputPin
	ComponentPlan   Plan
}

// Resolution is the immutable payload of a piglet-resolution record.
type Resolution struct {
	SourceDigest          string     `json:"sourceDigest"`
	EffectiveDigest       string     `json:"effectiveDigest"`
	GraphDigest           string     `json:"graphDigest"`
	ResourceClosureDigest string     `json:"resourceClosureDigest"`
	Inputs                []InputPin `json:"inputs"`
	ComponentPlan         Plan       `json:"componentPlan"`
}

// Artifact identifies a built Piglet artifact without coupling it to a local path.
type Artifact struct {
	Digest   string `json:"digest"`
	Size     int64  `json:"size"`
	FileName string `json:"fileName"`
}

// Verification is the mandatory verification result attached to an artifact.
type Verification struct {
	Policy string   `json:"policy"`
	Passed bool     `json:"passed"`
	Checks []string `json:"checks"`
}

// EnvironmentBinding pairs an artifact with its required runtime environment.
type EnvironmentBinding struct {
	Identity    string `json:"identity"`
	ImageDigest string `json:"imageDigest"`
}

// BinaryInput contains produced-artifact facts before resolution identities are derived.
type BinaryInput struct {
	Target            string
	PigVersion        string
	PigSourceRevision string
	PigSourceDigest   string
	Builder           string
	BuilderIdentity   string
	Toolchains        map[string]string
	Artifact          Artifact
	Verification      Verification
	Environment       *EnvironmentBinding
}

// Binary is the immutable payload of a piglet-binary record.
type Binary struct {
	ResolutionDigest      string              `json:"resolutionDigest"`
	GraphDigest           string              `json:"graphDigest"`
	ResourceClosureDigest string              `json:"resourceClosureDigest"`
	ComponentPlanDigest   string              `json:"componentPlanDigest"`
	Target                string              `json:"target"`
	PigVersion            string              `json:"pigVersion"`
	PigSourceRevision     string              `json:"pigSourceRevision"`
	PigSourceDigest       string              `json:"pigSourceDigest"`
	Builder               string              `json:"builder"`
	BuilderIdentity       string              `json:"builderIdentity"`
	Toolchains            map[string]string   `json:"toolchains"`
	Artifact              Artifact            `json:"artifact"`
	Verification          Verification        `json:"verification"`
	Environment           *EnvironmentBinding `json:"environment,omitempty"`
}

// Record is the first-production Piglet record envelope.
type Record struct {
	Kind           RecordKind  `json:"kind"`
	Piglet         string      `json:"piglet"`
	ReleaseVersion string      `json:"releaseVersion,omitempty"`
	CreatedAt      string      `json:"createdAt"`
	Resolution     *Resolution `json:"resolution,omitempty"`
	Binary         *Binary     `json:"binary,omitempty"`
	Digest         string      `json:"digest,omitempty"`
}

// NewResolutionRecord validates and canonicalizes one Piglet source resolution.
func NewResolutionRecord(piglet, releaseVersion string, createdAt time.Time, input ResolutionInput) (Record, error) {
	piglet = strings.TrimSpace(piglet)
	if piglet == "" || strings.ContainsAny(piglet, "/\\") {
		return Record{}, fmt.Errorf("Piglet name must be non-empty and cannot contain path separators")
	}
	releaseVersion = strings.TrimSpace(releaseVersion)
	if releaseVersion != "" && !semver.IsValid("v"+releaseVersion) {
		return Record{}, fmt.Errorf("Piglet release version %q is not valid SemVer", releaseVersion)
	}
	if createdAt.IsZero() {
		return Record{}, fmt.Errorf("Piglet record createdAt is required")
	}
	resolution, err := canonicalResolution(input)
	if err != nil {
		return Record{}, err
	}
	record := Record{
		Kind: RecordKindResolution, Piglet: piglet,
		ReleaseVersion: releaseVersion, CreatedAt: createdAt.UTC().Format(time.RFC3339),
		Resolution: &resolution,
	}
	record.Digest, err = recordDigest(record)
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

// NewBinaryRecord binds one built Piglet Binary to an exact resolution record.
func NewBinaryRecord(piglet, releaseVersion string, createdAt time.Time, resolution Record, input BinaryInput) (Record, error) {
	if err := ValidateRecord(resolution); err != nil {
		return Record{}, fmt.Errorf("Piglet Binary resolution record: %w", err)
	}
	if resolution.Kind != RecordKindResolution || resolution.Resolution == nil {
		return Record{}, fmt.Errorf("Piglet Binary requires a piglet-resolution record")
	}
	piglet = strings.TrimSpace(piglet)
	releaseVersion = strings.TrimSpace(releaseVersion)
	if piglet != resolution.Piglet || releaseVersion != resolution.ReleaseVersion {
		return Record{}, fmt.Errorf("Piglet Binary identity does not match its resolution record")
	}
	if createdAt.IsZero() {
		return Record{}, fmt.Errorf("Piglet record createdAt is required")
	}
	binary := Binary{
		ResolutionDigest: resolution.Digest, GraphDigest: resolution.Resolution.GraphDigest,
		ResourceClosureDigest: resolution.Resolution.ResourceClosureDigest,
		ComponentPlanDigest:   resolution.Resolution.ComponentPlan.Digest,
		Target:                strings.TrimSpace(input.Target), PigVersion: strings.TrimSpace(input.PigVersion),
		PigSourceRevision: strings.TrimSpace(input.PigSourceRevision), PigSourceDigest: strings.TrimSpace(input.PigSourceDigest),
		Builder: strings.TrimSpace(input.Builder), BuilderIdentity: strings.TrimSpace(input.BuilderIdentity),
		Toolchains: make(map[string]string, len(input.Toolchains)), Artifact: input.Artifact,
		Verification: input.Verification,
	}
	binary.Artifact.Digest = strings.TrimSpace(binary.Artifact.Digest)
	binary.Artifact.FileName = strings.TrimSpace(binary.Artifact.FileName)
	binary.Verification.Policy = strings.TrimSpace(binary.Verification.Policy)
	binary.Verification.Checks = append([]string{}, binary.Verification.Checks...)
	slices.Sort(binary.Verification.Checks)
	if input.Environment != nil {
		environment := *input.Environment
		environment.Identity = strings.TrimSpace(environment.Identity)
		environment.ImageDigest = strings.TrimSpace(environment.ImageDigest)
		binary.Environment = &environment
	}
	for name, version := range input.Toolchains {
		name = strings.TrimSpace(name)
		if _, duplicate := binary.Toolchains[name]; duplicate {
			return Record{}, fmt.Errorf("Piglet Binary has duplicate toolchain %q", name)
		}
		binary.Toolchains[name] = strings.TrimSpace(version)
	}
	if err := validateBinary(binary); err != nil {
		return Record{}, err
	}
	record := Record{
		Kind: RecordKindBinary, Piglet: piglet,
		ReleaseVersion: releaseVersion, CreatedAt: createdAt.UTC().Format(time.RFC3339), Binary: &binary,
	}
	var err error
	record.Digest, err = recordDigest(record)
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

// ParseRecord decodes one closed Piglet record document and verifies its
// canonical payload and digest.
func ParseRecord(data []byte) (Record, error) {
	var record Record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, fmt.Errorf("parse Piglet record: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Record{}, fmt.Errorf("parse Piglet record: trailing content")
	}
	if err := ValidateRecord(record); err != nil {
		return Record{}, err
	}
	return record, nil
}

// ValidateBinaryLink verifies that one Piglet Binary record references the
// exact supplied resolution record and repeats its derived graph identities.
func ValidateBinaryLink(resolution, binary Record) error {
	if err := ValidateRecord(resolution); err != nil {
		return fmt.Errorf("Piglet resolution record: %w", err)
	}
	if err := ValidateRecord(binary); err != nil {
		return fmt.Errorf("Piglet Binary record: %w", err)
	}
	if resolution.Kind != RecordKindResolution || resolution.Resolution == nil {
		return fmt.Errorf("expected piglet-resolution record, got %q", resolution.Kind)
	}
	if binary.Kind != RecordKindBinary || binary.Binary == nil {
		return fmt.Errorf("expected piglet-binary record, got %q", binary.Kind)
	}
	if binary.Piglet != resolution.Piglet || binary.ReleaseVersion != resolution.ReleaseVersion {
		return fmt.Errorf("Piglet Binary identity does not match its resolution record")
	}
	switch {
	case binary.Binary.ResolutionDigest != resolution.Digest:
		return fmt.Errorf("Piglet Binary references a different resolution")
	case binary.Binary.GraphDigest != resolution.Resolution.GraphDigest:
		return fmt.Errorf("Piglet Binary references a different Piglet graph")
	case binary.Binary.ResourceClosureDigest != resolution.Resolution.ResourceClosureDigest:
		return fmt.Errorf("Piglet Binary references a different Resource closure")
	case binary.Binary.ComponentPlanDigest != resolution.Resolution.ComponentPlan.Digest:
		return fmt.Errorf("Piglet Binary references a different component plan")
	}
	return nil
}

// ValidateRecord verifies a decoded Piglet record's canonical shape and digest.
func ValidateRecord(record Record) error {
	if record.Piglet == "" || record.Piglet != strings.TrimSpace(record.Piglet) || strings.ContainsAny(record.Piglet, "/\\") {
		return fmt.Errorf("Piglet record has invalid Piglet name %q", record.Piglet)
	}
	if record.ReleaseVersion != "" && (!semver.IsValid("v"+record.ReleaseVersion) || record.ReleaseVersion != strings.TrimSpace(record.ReleaseVersion)) {
		return fmt.Errorf("Piglet record has invalid release version %q", record.ReleaseVersion)
	}
	createdAt, err := time.Parse(time.RFC3339, record.CreatedAt)
	if err != nil || createdAt.UTC().Format(time.RFC3339) != record.CreatedAt {
		return fmt.Errorf("Piglet record has invalid createdAt %q", record.CreatedAt)
	}
	if err := validateDigest(record.Digest); err != nil {
		return fmt.Errorf("Piglet record digest: %w", err)
	}
	switch record.Kind {
	case RecordKindResolution:
		if record.Resolution == nil || record.Binary != nil {
			return fmt.Errorf("piglet-resolution record has invalid payloads")
		}
		if err := validateResolution(*record.Resolution); err != nil {
			return err
		}
	case RecordKindBinary:
		if record.Binary == nil || record.Resolution != nil {
			return fmt.Errorf("piglet-binary record has invalid payloads")
		}
		if err := validateBinary(*record.Binary); err != nil {
			return err
		}
	case RecordKindImage:
		return fmt.Errorf("Piglet record kind %q payload is not implemented", record.Kind)
	default:
		return fmt.Errorf("Piglet record kind %q is unsupported", record.Kind)
	}
	digest, err := recordDigest(record)
	if err != nil {
		return err
	}
	if record.Digest != digest {
		return fmt.Errorf("Piglet record digest mismatch: got %q, want %q", record.Digest, digest)
	}
	return nil
}

func validateBinary(binary Binary) error {
	for _, check := range []struct {
		name   string
		digest string
	}{
		{"resolution", binary.ResolutionDigest},
		{"graph", binary.GraphDigest},
		{"Resource closure", binary.ResourceClosureDigest},
		{"component plan", binary.ComponentPlanDigest},
		{"Pig source", binary.PigSourceDigest},
		{"artifact", binary.Artifact.Digest},
	} {
		if err := validateDigest(check.digest); err != nil {
			return fmt.Errorf("Piglet Binary %s digest: %w", check.name, err)
		}
	}
	if binary.Target == "" || binary.Target != strings.TrimSpace(binary.Target) {
		return fmt.Errorf("Piglet Binary target is required and canonical")
	}
	if os, arch, ok := strings.Cut(binary.Target, "/"); !ok || os == "" || arch == "" {
		return fmt.Errorf("Piglet Binary target %q must be os/arch", binary.Target)
	}
	if binary.PigVersion == "" || binary.PigSourceRevision == "" || binary.Builder == "" || binary.BuilderIdentity == "" {
		return fmt.Errorf("Piglet Binary Pig and builder identities are required")
	}
	if binary.PigVersion != strings.TrimSpace(binary.PigVersion) || binary.PigSourceRevision != strings.TrimSpace(binary.PigSourceRevision) || binary.Builder != strings.TrimSpace(binary.Builder) || binary.BuilderIdentity != strings.TrimSpace(binary.BuilderIdentity) {
		return fmt.Errorf("Piglet Binary Pig or builder identity is not canonical")
	}
	for name, version := range binary.Toolchains {
		if name == "" || version == "" || name != strings.TrimSpace(name) || version != strings.TrimSpace(version) {
			return fmt.Errorf("Piglet Binary toolchain identity is empty or non-canonical")
		}
	}
	if binary.Artifact.Size <= 0 || binary.Artifact.FileName == "" || binary.Artifact.FileName != strings.TrimSpace(binary.Artifact.FileName) || strings.ContainsAny(binary.Artifact.FileName, "/\\") {
		return fmt.Errorf("Piglet Binary artifact size or filename is invalid")
	}
	if binary.Verification.Policy != "basic" || !binary.Verification.Passed || !slices.IsSorted(binary.Verification.Checks) || !slices.Contains(binary.Verification.Checks, "artifact-sha256") || !slices.Contains(binary.Verification.Checks, "artifact-version-smoke") {
		return fmt.Errorf("Piglet Binary mandatory basic verification is invalid")
	}
	for i, check := range binary.Verification.Checks {
		if check == "" || check != strings.TrimSpace(check) || (i > 0 && check == binary.Verification.Checks[i-1]) {
			return fmt.Errorf("Piglet Binary verification checks are empty, duplicate, or non-canonical")
		}
	}
	if binary.Environment != nil {
		if err := validateDigest(binary.Environment.Identity); err != nil {
			return fmt.Errorf("Piglet Binary environment identity: %w", err)
		}
		if err := validateDigest(binary.Environment.ImageDigest); err != nil {
			return fmt.Errorf("Piglet Binary environment image: %w", err)
		}
	}
	return nil
}

func canonicalResolution(input ResolutionInput) (Resolution, error) {
	inputs := append([]InputPin{}, input.Inputs...)
	for i := range inputs {
		inputs[i].Kind = strings.TrimSpace(inputs[i].Kind)
		inputs[i].Name = strings.TrimSpace(inputs[i].Name)
		inputs[i].Source = strings.TrimSpace(inputs[i].Source)
		inputs[i].Version = strings.TrimSpace(inputs[i].Version)
		inputs[i].Digest = strings.TrimSpace(inputs[i].Digest)
		inputs[i].Package = strings.TrimSpace(inputs[i].Package)
		inputs[i].Plugin = strings.TrimSpace(inputs[i].Plugin)
	}
	slices.SortFunc(inputs, compareInputPins)
	componentPlan := input.ComponentPlan
	componentPlan.Components = slices.Clone(input.ComponentPlan.Components)
	resolution := Resolution{
		SourceDigest:    strings.TrimSpace(input.SourceDigest),
		EffectiveDigest: strings.TrimSpace(input.EffectiveDigest), Inputs: inputs,
		ComponentPlan: componentPlan,
	}
	resourceDigest, err := digestValue(inputs)
	if err != nil {
		return Resolution{}, fmt.Errorf("digest Piglet Resource closure: %w", err)
	}
	resolution.ResourceClosureDigest = resourceDigest
	resolution.GraphDigest, err = digestValue(struct {
		SourceDigest          string `json:"sourceDigest"`
		EffectiveDigest       string `json:"effectiveDigest"`
		ResourceClosureDigest string `json:"resourceClosureDigest"`
		ComponentPlanDigest   string `json:"componentPlanDigest"`
	}{resolution.SourceDigest, resolution.EffectiveDigest, resolution.ResourceClosureDigest, resolution.ComponentPlan.Digest})
	if err != nil {
		return Resolution{}, fmt.Errorf("digest Piglet graph: %w", err)
	}
	if err := validateResolution(resolution); err != nil {
		return Resolution{}, err
	}
	return resolution, nil
}

func validateResolution(resolution Resolution) error {
	for _, check := range []struct {
		name   string
		digest string
	}{
		{"source", resolution.SourceDigest},
		{"effective", resolution.EffectiveDigest},
		{"graph", resolution.GraphDigest},
		{"Resource closure", resolution.ResourceClosureDigest},
	} {
		if err := validateDigest(check.digest); err != nil {
			return fmt.Errorf("Piglet resolution %s digest: %w", check.name, err)
		}
	}
	if !slices.IsSortedFunc(resolution.Inputs, compareInputPins) {
		return fmt.Errorf("Piglet resolution inputs are not in canonical order")
	}
	for i, input := range resolution.Inputs {
		if err := validateInputPin(input); err != nil {
			return err
		}
		if i > 0 && input.Kind == resolution.Inputs[i-1].Kind && input.Name == resolution.Inputs[i-1].Name {
			return fmt.Errorf("duplicate input %q", input.Kind+"/"+input.Name)
		}
	}
	if err := ValidatePlan(resolution.ComponentPlan); err != nil {
		return fmt.Errorf("Piglet resolution component plan: %w", err)
	}
	resourceDigest, err := digestValue(resolution.Inputs)
	if err != nil {
		return fmt.Errorf("digest Piglet Resource closure: %w", err)
	}
	if resourceDigest != resolution.ResourceClosureDigest {
		return fmt.Errorf("Piglet resolution Resource closure digest mismatch")
	}
	graphDigest, err := digestValue(struct {
		SourceDigest          string `json:"sourceDigest"`
		EffectiveDigest       string `json:"effectiveDigest"`
		ResourceClosureDigest string `json:"resourceClosureDigest"`
		ComponentPlanDigest   string `json:"componentPlanDigest"`
	}{resolution.SourceDigest, resolution.EffectiveDigest, resolution.ResourceClosureDigest, resolution.ComponentPlan.Digest})
	if err != nil {
		return fmt.Errorf("digest Piglet graph: %w", err)
	}
	if graphDigest != resolution.GraphDigest {
		return fmt.Errorf("Piglet resolution graph digest mismatch")
	}
	return nil
}

func validateInputPin(input InputPin) error {
	identity := input.Kind + "/" + input.Name
	if input.Kind == "" || input.Name == "" || input.Source == "" {
		return fmt.Errorf("Piglet input %q has empty identity or source", identity)
	}
	if input.Kind != strings.TrimSpace(input.Kind) || input.Name != strings.TrimSpace(input.Name) || input.Source != strings.TrimSpace(input.Source) || input.Version != strings.TrimSpace(input.Version) || input.Digest != strings.TrimSpace(input.Digest) || input.Package != strings.TrimSpace(input.Package) || input.Plugin != strings.TrimSpace(input.Plugin) {
		return fmt.Errorf("Piglet input %q is not canonical", identity)
	}
	if strings.ContainsAny(input.Kind, "/\\") || strings.ContainsAny(input.Name, "/\\") {
		return fmt.Errorf("Piglet input %q contains path separators", identity)
	}
	if input.Package != "" && input.Plugin != "" {
		return fmt.Errorf("Piglet input %q cannot declare both package and plugin membership", identity)
	}
	if err := validateDigest(input.Digest); err != nil {
		return fmt.Errorf("Piglet input %q digest: %w", identity, err)
	}
	return nil
}

func compareInputPins(a, b InputPin) int {
	return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name), cmp.Compare(a.Source, b.Source))
}

func recordDigest(record Record) (string, error) {
	copy := record
	copy.Digest = ""
	return digestValue(copy)
}

func digestValue(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
