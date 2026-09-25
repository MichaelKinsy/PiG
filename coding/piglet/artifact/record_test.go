package artifact

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAC9PigletRecordCurrentShape(t *testing.T) {
	t.Parallel()

	componentPlan, err := BuildPlan([]ComponentInput{{
		Kind: ComponentKindExtension, Name: "review", Language: "go", Fusible: true,
		Origin: Origin{Source: "package:base", Package: "base", Digest: testDigest},
	}})
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	record, err := NewResolutionRecord("review", "1.2.0", createdAt, ResolutionInput{
		SourceDigest:    "sha256:" + strings.Repeat("b", 64),
		EffectiveDigest: "sha256:" + strings.Repeat("c", 64),
		ComponentPlan:   componentPlan,
		Inputs: []InputPin{
			{Kind: "skill", Name: "commit", Source: "piglet:skills/commit", Digest: "sha256:" + strings.Repeat("f", 64)},
			{Kind: "package", Name: "base", Source: "npm:@example/base", Version: "1.0.0", Digest: testDigest},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Kind != RecordKindResolution || record.Piglet != "review" || record.ReleaseVersion != "1.2.0" {
		t.Fatalf("record envelope = %#v", record)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["version"]; exists {
		t.Fatalf("record exposed a Pig-owned format version: %s", encoded)
	}
	resolutionDocument, _ := document["resolution"].(map[string]any)
	if _, exists := resolutionDocument["schemaVersion"]; exists {
		t.Fatalf("resolution exposed a Pig-owned schema version: %s", encoded)
	}
	document["version"] = 1
	formerRecord, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRecord(formerRecord); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("former record version field error = %v", err)
	}
	delete(document, "version")
	resolutionDocument["schemaVersion"] = 1
	formerResolution, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRecord(formerResolution); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("former resolution schemaVersion field error = %v", err)
	}
	if record.CreatedAt != "2026-07-31T12:00:00Z" {
		t.Fatalf("createdAt = %q", record.CreatedAt)
	}
	if !strings.HasPrefix(record.Digest, "sha256:") || len(record.Digest) != 71 {
		t.Fatalf("digest = %q", record.Digest)
	}
	if got := record.Resolution.Inputs[0].Kind + "/" + record.Resolution.Inputs[0].Name; got != "package/base" {
		t.Fatalf("first input = %q, want package/base", got)
	}
	if err := ValidateRecord(record); err != nil {
		t.Fatalf("ValidateRecord() error = %v", err)
	}

	reordered := record
	reordered.Resolution = cloneResolution(record.Resolution)
	slices.Reverse(reordered.Resolution.Inputs)
	if err := ValidateRecord(reordered); err == nil || !strings.Contains(err.Error(), "canonical order") {
		t.Fatalf("reordered ValidateRecord() error = %v", err)
	}

	tampered := record
	tampered.Resolution = cloneResolution(record.Resolution)
	tampered.Resolution.Inputs[0].Digest = "sha256:" + strings.Repeat("0", 64)
	if err := ValidateRecord(tampered); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered ValidateRecord() error = %v", err)
	}
}

func TestAC9PigletBinaryRecordCurrentShape(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	resolution, err := NewResolutionRecord("review", "1.2.0", createdAt, validResolutionInput(t))
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewBinaryRecord("review", "1.2.0", createdAt, resolution, BinaryInput{
		Target:     "linux/amd64",
		PigVersion: "0.81.1", PigSourceRevision: "revision", PigSourceDigest: "sha256:" + strings.Repeat("1", 64),
		Builder: "native", BuilderIdentity: "native:revision", Toolchains: map[string]string{"go": "go version go1.26"},
		Artifact:     Artifact{Digest: "sha256:" + strings.Repeat("2", 64), Size: 42, FileName: "pig-review"},
		Verification: Verification{Policy: "basic", Passed: true, Checks: []string{"artifact-sha256", "artifact-version-smoke"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Kind != RecordKindBinary || record.Binary == nil {
		t.Fatalf("binary record = %#v", record)
	}
	if record.Binary.ResolutionDigest != resolution.Digest || record.Binary.GraphDigest != resolution.Resolution.GraphDigest || record.Binary.ComponentPlanDigest != resolution.Resolution.ComponentPlan.Digest {
		t.Fatalf("binary identity = %#v", record.Binary)
	}
	if err := ValidateRecord(record); err != nil {
		t.Fatalf("ValidateRecord() error = %v", err)
	}

	tampered := record
	binary := *record.Binary
	binary.Artifact.Digest = "sha256:" + strings.Repeat("3", 64)
	tampered.Binary = &binary
	if err := ValidateRecord(tampered); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered ValidateRecord() error = %v", err)
	}
}

func TestParseRecordAndBinaryCrossLink(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	resolution, err := NewResolutionRecord("review", "1.2.0", createdAt, validResolutionInput(t))
	if err != nil {
		t.Fatal(err)
	}
	binary, err := NewBinaryRecord("review", "1.2.0", createdAt, resolution, BinaryInput{
		Target: "linux/amd64", PigVersion: "0.81.1", PigSourceRevision: "revision", PigSourceDigest: "sha256:" + strings.Repeat("1", 64),
		Builder: "native", BuilderIdentity: "native:revision", Toolchains: map[string]string{"go": "go version go1.26"},
		Artifact:     Artifact{Digest: "sha256:" + strings.Repeat("2", 64), Size: 42, FileName: "pig-review"},
		Verification: Verification{Policy: "basic", Passed: true, Checks: []string{"artifact-sha256", "artifact-version-smoke"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(binary)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRecord(data)
	if err != nil || parsed.Digest != binary.Digest {
		t.Fatalf("ParseRecord() = %#v, %v", parsed, err)
	}
	if _, err := ParseRecord(append(data, []byte("\n{}")...)); err == nil || !strings.Contains(err.Error(), "trailing content") {
		t.Fatalf("trailing content error = %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["legacyReceipt"] = true
	unknown, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRecord(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}

	changedInput := validResolutionInput(t)
	changedInput.EffectiveDigest = "sha256:" + strings.Repeat("9", 64)
	otherResolution, err := NewResolutionRecord("review", "1.2.0", createdAt.Add(time.Second), changedInput)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateBinaryLink(otherResolution, binary); err == nil || !strings.Contains(err.Error(), "different resolution") {
		t.Fatalf("cross-link error = %v", err)
	}
}

func TestAC9PigletRecordCurrentShapeRejectsInvalidResolution(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		piglet     string
		resolution ResolutionInput
		want       string
	}{
		{name: "empty piglet", resolution: validResolutionInput(t), want: "Piglet name"},
		{name: "bad source digest", piglet: "review", resolution: func() ResolutionInput { r := validResolutionInput(t); r.SourceDigest = "sha256:no"; return r }(), want: "source digest"},
		{name: "duplicate input", piglet: "review", resolution: func() ResolutionInput {
			r := validResolutionInput(t)
			r.Inputs = append(r.Inputs, r.Inputs[0])
			return r
		}(), want: "duplicate input"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewResolutionRecord(test.piglet, "", createdAt, test.resolution)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewResolutionRecord() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func validResolutionInput(t *testing.T) ResolutionInput {
	t.Helper()
	plan, err := BuildPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	return ResolutionInput{
		SourceDigest:    "sha256:" + strings.Repeat("a", 64),
		EffectiveDigest: "sha256:" + strings.Repeat("b", 64),
		ComponentPlan:   plan,
		Inputs: []InputPin{{
			Kind: "package", Name: "base", Source: "npm:@example/base", Digest: "sha256:" + strings.Repeat("e", 64),
		}},
	}
}

func cloneResolution(source *Resolution) *Resolution {
	clone := *source
	clone.Inputs = slices.Clone(source.Inputs)
	clone.ComponentPlan.Components = slices.Clone(source.ComponentPlan.Components)
	return &clone
}
