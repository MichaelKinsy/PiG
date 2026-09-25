package artifact

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const testDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestAC2ComponentRealizationPlan(t *testing.T) {
	t.Parallel()

	inputs := []ComponentInput{
		{
			Kind: "extension", Name: "review", Language: "go", Fusible: true,
			Origin: Origin{Source: "git:https://example.com/review.git", Digest: testDigest, Package: "base"},
		},
		{
			Kind: "extension", Name: "isolated-go", Language: "go", Fusible: true,
			IsolationRequired: true, Materialization: MaterializationRelease,
			Origin: Origin{Source: "npm:@example/isolated-go@1.0.0", Digest: testDigest, Plugin: "review-tools"},
		},
		{
			Kind: "extension", Name: "rust-edit", Language: "rust",
			Materialization: MaterializationRelease,
			Origin:          Origin{Source: "git:https://example.com/rust-edit.git@v1", Digest: testDigest},
		},
		{
			Kind: "extension", Name: "python-analysis", Language: "python",
			Materialization: MaterializationAgentEnvironment,
			Origin:          Origin{Source: "piglet:extensions/python-analysis", Digest: testDigest},
			Runtime:         &RuntimeRequirement{Name: "python", Version: "3.12", Target: "linux/amd64"},
		},
		{
			Kind: "mcp", Name: "source-control", External: true,
			Materialization: MaterializationExternal,
			Origin:          Origin{Source: "https://mcp.example.com", Digest: testDigest},
		},
	}

	plan, err := BuildPlan(inputs)
	if err != nil {
		t.Fatalf("BuildPlan() error = %v", err)
	}

	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	if _, exists := document["version"]; exists {
		t.Fatalf("component plan exposed a Pig-owned format version: %s", encoded)
	}
	if !strings.HasPrefix(plan.Digest, "sha256:") || len(plan.Digest) != 71 {
		t.Fatalf("Digest = %q, want sha256 plus 64 hex characters", plan.Digest)
	}

	want := map[string]struct {
		realization     Realization
		materialization Materialization
	}{
		"extension/review":          {RealizationFused, MaterializationBinary},
		"extension/isolated-go":     {RealizationSubprocess, MaterializationRelease},
		"extension/rust-edit":       {RealizationSubprocess, MaterializationRelease},
		"extension/python-analysis": {RealizationSubprocess, MaterializationAgentEnvironment},
		"mcp/source-control":        {RealizationExternal, MaterializationExternal},
	}
	for _, component := range plan.Components {
		expected, ok := want[component.ID()]
		if !ok {
			t.Fatalf("unexpected component %q", component.ID())
		}
		if component.Realization != expected.realization {
			t.Errorf("%s realization = %q, want %q", component.ID(), component.Realization, expected.realization)
		}
		if component.Materialization != expected.materialization {
			t.Errorf("%s materialization = %q, want %q", component.ID(), component.Materialization, expected.materialization)
		}
	}

	reversed := slices.Clone(inputs)
	slices.Reverse(reversed)
	reordered, err := BuildPlan(reversed)
	if err != nil {
		t.Fatalf("BuildPlan(reversed) error = %v", err)
	}
	if reordered.Digest != plan.Digest {
		t.Fatalf("reordered digest = %q, want %q", reordered.Digest, plan.Digest)
	}
	for i := range plan.Components {
		if reordered.Components[i].ID() != plan.Components[i].ID() {
			t.Fatalf("reordered component[%d] = %q, want %q", i, reordered.Components[i].ID(), plan.Components[i].ID())
		}
	}
}

func TestAC7ValidatePlanRejectsTampering(t *testing.T) {
	t.Parallel()

	plan, err := BuildPlan([]ComponentInput{{
		Kind: "extension", Name: "review", Language: "go", Fusible: true,
		Origin: Origin{Source: "piglet:review", Digest: testDigest},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlan(plan); err != nil {
		t.Fatalf("ValidatePlan() error = %v", err)
	}

	tampered := plan
	tampered.Components = slices.Clone(plan.Components)
	tampered.Components[0].Name = "other"
	if err := ValidatePlan(tampered); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("tampered ValidatePlan() error = %v", err)
	}

	invalid := plan
	invalid.Components = slices.Clone(plan.Components)
	invalid.Components[0].Realization = RealizationSubprocess
	invalid.Components[0].Materialization = ""
	if err := ValidatePlan(invalid); err == nil || !strings.Contains(err.Error(), "subprocess materialization") {
		t.Fatalf("invalid ValidatePlan() error = %v", err)
	}
}

func TestAC2ComponentRealizationPlanRejectsInvalidClosure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []ComponentInput
		want  string
	}{
		{
			name: "non-executable resource kind",
			input: []ComponentInput{{
				Kind: "skill", Name: "review", External: true,
				Materialization: MaterializationExternal,
				Origin:          Origin{Source: "piglet:skills/review", Digest: testDigest},
			}},
			want: "unsupported executable component kind",
		},
		{
			name: "missing origin digest",
			input: []ComponentInput{{
				Kind: "extension", Name: "review", Language: "go", Fusible: true,
				Origin: Origin{Source: "piglet:review"},
			}},
			want: "origin digest",
		},
		{
			name: "subprocess without materialization",
			input: []ComponentInput{{
				Kind: "extension", Name: "python", Language: "python",
				Origin: Origin{Source: "piglet:python", Digest: testDigest},
			}},
			want: "materialization",
		},
		{
			name: "python subprocess without runtime",
			input: []ComponentInput{{
				Kind: "extension", Name: "python", Language: "python",
				Materialization: MaterializationRelease,
				Origin:          Origin{Source: "piglet:python", Digest: testDigest},
			}},
			want: "requires a runtime",
		},
		{
			name: "fused component with runtime",
			input: []ComponentInput{{
				Kind: "extension", Name: "go", Language: "go", Fusible: true,
				Origin:  Origin{Source: "piglet:go", Digest: testDigest},
				Runtime: &RuntimeRequirement{Name: "go"},
			}},
			want: "cannot require a language runtime",
		},
		{
			name: "external in release",
			input: []ComponentInput{{
				Kind: "mcp", Name: "remote", External: true,
				Materialization: MaterializationRelease,
				Origin:          Origin{Source: "https://mcp.example.com", Digest: testDigest},
			}},
			want: "external",
		},
		{
			name: "package and plugin membership",
			input: []ComponentInput{{
				Kind: "extension", Name: "review", Language: "go", Fusible: true,
				Origin: Origin{Source: "piglet:review", Digest: testDigest, Package: "base", Plugin: "review"},
			}},
			want: "package and plugin",
		},
		{
			name: "duplicate identity",
			input: []ComponentInput{
				{Kind: "extension", Name: "review", Language: "go", Fusible: true, Origin: Origin{Source: "piglet:a", Digest: testDigest}},
				{Kind: "extension", Name: "review", Language: "go", Fusible: true, Origin: Origin{Source: "piglet:b", Digest: testDigest}},
			},
			want: "duplicate component",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := BuildPlan(test.input)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildPlan() error = %v, want containing %q", err, test.want)
			}
		})
	}
}
