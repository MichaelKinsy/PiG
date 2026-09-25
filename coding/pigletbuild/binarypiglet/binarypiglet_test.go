package binarypiglet

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/piglet/artifact"
)

// TestParse pins the fallback contract: only a baked piglet with a model or a
// prompt yields a non-nil default; everything else (empty, comment-only, or a
// name-only piglet) resolves to nil so normal startup runs.
func TestParse(t *testing.T) {
	cases := map[string]struct {
		yaml   string
		wantOK bool
	}{
		"empty":        {"", false},
		"comment only": {"# nothing baked\n", false},
		"name only":    {"name: x\n", false},
		"with model":   {"name: x\nmodel:\n  provider: github-copilot\n  name: gpt-5-mini\n", true},
		"with prompt":  {"name: x\nsystemPrompt:\n  text: hello\n", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := parse([]byte(tc.yaml))
			if tc.wantOK && got == nil {
				t.Fatalf("parse(%q) = nil, want a piglet", tc.yaml)
			}
			if !tc.wantOK && got != nil {
				t.Fatalf("parse(%q) = %+v, want nil", tc.yaml, got)
			}
		})
	}
}

// TestDefault_StockIsNil: the committed piglet.yaml bakes nothing, so a stock
// Stock Pig and a Binary built without an embedded Piglet get no default.
func TestDefault_StockIsNil(t *testing.T) {
	if got := Default(); got != nil {
		t.Fatalf("Default() = %+v on committed piglet.yaml, want nil", got)
	}
}

// swapBaked temporarily replaces the embedded piglet and closure so Verify can
// be exercised without compiling a real Piglet Binary.
func swapBaked(t *testing.T, yaml, closure []byte) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	oy, oc := pigletYAML, resolutionRecordJSON
	pigletYAML, resolutionRecordJSON = yaml, closure
	t.Cleanup(func() { pigletYAML, resolutionRecordJSON = oy, oc })
}

// resolutionFor builds a valid resolution record whose effective digest matches
// the given baked piglet bytes, exactly as a real build would.
func resolutionFor(t *testing.T, yaml []byte) artifact.Record {
	t.Helper()
	plan, err := artifact.BuildPlan(nil)
	if err != nil {
		t.Fatal(err)
	}
	record, err := artifact.NewResolutionRecord("t", "1.0.0", time.Unix(1, 0), artifact.ResolutionInput{
		SourceDigest:    "sha256:" + strings.Repeat("a", 64),
		EffectiveDigest: "sha256:" + hex.EncodeToString(sha256Sum(yaml)),
		ComponentPlan:   plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestVerify_StockIsNoop: with no baked closure, Verify passes so stock pig and
// Stock Pig builds without a resolution record run unchanged.
func TestVerify_StockIsNoop(t *testing.T) {
	swapBaked(t, nil, nil)
	if err := Verify(); err != nil {
		t.Fatalf("Verify() with no closure = %v, want nil", err)
	}
}

// TestIsPigletBinary: a baked closure marks the executable as a Piglet Binary;
// stock pig (no closure) is not.
func TestIsPigletBinary(t *testing.T) {
	swapBaked(t, nil, nil)
	if IsPigletBinary() {
		t.Fatal("IsPigletBinary() = true with no baked closure, want false")
	}
	swapBaked(t, []byte("name: t\n"), []byte(`{"kind":"piglet-resolution"}`))
	if !IsPigletBinary() {
		t.Fatal("IsPigletBinary() = false with a baked closure, want true")
	}
}

// TestVerify_ValidClosurePasses: a well-formed resolution record whose effective
// digest matches the baked piglet verifies.
func TestVerify_ValidClosurePasses(t *testing.T) {
	yaml := []byte("name: t\n")
	swapBaked(t, yaml, marshal(t, resolutionFor(t, yaml)))
	if err := Verify(); err != nil {
		t.Fatalf("Verify() on a valid closure = %v, want nil", err)
	}
}

// TestVerify_PigletDigestMismatchFails: a closure whose effective digest does
// not match the baked piglet is rejected (piglet swapped after the build).
func TestVerify_PigletDigestMismatchFails(t *testing.T) {
	closure := marshal(t, resolutionFor(t, []byte("name: built\n")))
	swapBaked(t, []byte("name: swapped\n"), closure)
	err := Verify()
	if err == nil || !strings.Contains(err.Error(), "does not match its closure") {
		t.Fatalf("Verify() = %v, want a piglet/closure mismatch error", err)
	}
}

// TestVerify_TamperedRecordFails: mutating a signed resolution field after the
// record is built breaks the envelope digest, so Verify refuses to run.
func TestVerify_TamperedRecordFails(t *testing.T) {
	yaml := []byte("name: t\n")
	var document map[string]any
	if err := json.Unmarshal(marshal(t, resolutionFor(t, yaml)), &document); err != nil {
		t.Fatal(err)
	}
	resolution := document["resolution"].(map[string]any)
	resolution["sourceDigest"] = "sha256:" + strings.Repeat("b", 64)
	swapBaked(t, yaml, marshal(t, document))
	err := Verify()
	if err == nil || !strings.Contains(err.Error(), "failed verification") {
		t.Fatalf("Verify() = %v, want a verification failure", err)
	}
}

// TestVerify_MalformedClosureFails: a non-empty but undecodable closure is a
// fail-closed condition.
func TestVerify_MalformedClosureFails(t *testing.T) {
	swapBaked(t, []byte("name: t\n"), []byte("{ not json"))
	err := Verify()
	if err == nil || !strings.Contains(err.Error(), "decode baked Piglet closure") {
		t.Fatalf("Verify() = %v, want a decode error", err)
	}
}

// planWithBuiltIns builds a plan carrying one fused and one binary-packed Go
// extension and returns the record plus the names the runtime must register.
func planWithBuiltIns(t *testing.T) (artifact.Record, []string, []string) {
	t.Helper()
	plan, err := artifact.BuildPlan([]artifact.ComponentInput{
		{Kind: artifact.ComponentKindExtension, Name: "fx", Language: "go", Fusible: true, Materialization: artifact.MaterializationBinary, Origin: artifact.Origin{Source: "local", Digest: "sha256:" + strings.Repeat("d", 64)}},
		{Kind: artifact.ComponentKindExtension, Name: "px", Language: "go", Materialization: artifact.MaterializationBinary, Origin: artifact.Origin{Source: "local", Digest: "sha256:" + strings.Repeat("e", 64)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var fused, packed []string
	for _, component := range plan.Components {
		switch {
		case component.Realization == artifact.RealizationFused:
			fused = append(fused, component.Name)
		case component.Realization == artifact.RealizationSubprocess && component.Materialization == artifact.MaterializationBinary:
			packed = append(packed, component.Name)
		}
	}
	if len(fused) != 1 || fused[0] != "fx" || len(packed) != 1 || packed[0] != "px" {
		t.Fatalf("unexpected plan realization: fused=%v packed=%v", fused, packed)
	}
	record, err := artifact.NewResolutionRecord("t", "1.0.0", time.Unix(1, 0), artifact.ResolutionInput{
		SourceDigest:    "sha256:" + strings.Repeat("a", 64),
		EffectiveDigest: "sha256:" + strings.Repeat("c", 64),
		ComponentPlan:   plan,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record, fused, packed
}

// TestVerifyRegisteredClosure_StockIsNoop: with no baked closure, the registered
// closure check passes regardless of what is registered.
func TestVerifyRegisteredClosure_StockIsNoop(t *testing.T) {
	swapBaked(t, nil, nil)
	if err := VerifyRegisteredClosure([]string{"anything"}, []string{"else"}); err != nil {
		t.Fatalf("VerifyRegisteredClosure() with no closure = %v, want nil", err)
	}
}

// TestVerifyRegisteredClosure_MatchPasses: registering exactly the planned
// fused and packed extensions verifies.
func TestVerifyRegisteredClosure_MatchPasses(t *testing.T) {
	record, fused, packed := planWithBuiltIns(t)
	swapBaked(t, []byte("name: t\n"), marshal(t, record))
	if err := VerifyRegisteredClosure(fused, packed); err != nil {
		t.Fatalf("VerifyRegisteredClosure(match) = %v, want nil", err)
	}
}

// TestVerifyRegisteredClosure_MissingFusedFails: a planned fused extension that
// is not registered (tampered fuse registry, or a substitute) fails closed.
func TestVerifyRegisteredClosure_MissingFusedFails(t *testing.T) {
	record, _, packed := planWithBuiltIns(t)
	swapBaked(t, []byte("name: t\n"), marshal(t, record))
	err := VerifyRegisteredClosure(nil, packed)
	if err == nil || !strings.Contains(err.Error(), "fused") || !strings.Contains(err.Error(), "missing [fx]") {
		t.Fatalf("VerifyRegisteredClosure(missing fused) = %v, want a missing-fused error", err)
	}
}

// TestVerifyRegisteredClosure_UnexpectedPackedFails: a packed extension the plan
// did not declare (extra cell) fails closed.
func TestVerifyRegisteredClosure_UnexpectedPackedFails(t *testing.T) {
	record, fused, packed := planWithBuiltIns(t)
	swapBaked(t, []byte("name: t\n"), marshal(t, record))
	err := VerifyRegisteredClosure(fused, append(packed, "ghost"))
	if err == nil || !strings.Contains(err.Error(), "binary-packed") || !strings.Contains(err.Error(), "unexpected [ghost]") {
		t.Fatalf("VerifyRegisteredClosure(unexpected packed) = %v, want an unexpected-packed error", err)
	}
}
