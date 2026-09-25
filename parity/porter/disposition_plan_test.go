package porter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// TestDispositionPlanGroupsMaterialPortedChanges pins the acceptance behaviour:
// disposition-plan surfaces only changes that are structurally material AND land
// on a PORT_MAP-ported file, grouped by shape-change theme. Cosmetic hash churn,
// non-ported files, and added/removed declarations are excluded.
func TestDispositionPlanGroupsMaterialPortedChanges(t *testing.T) {
	root := t.TempDir()
	to := coding.UpstreamVersion
	from := "0.83.0"
	dir := filepath.Join(root, "parity", "interfaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Four "changed" declarations exercise every filter branch:
	//   ported-material  : gains a property on a ported (✅) file  -> surfaced
	//   ported-cosmetic  : same structure, different hash          -> dropped (not material)
	//   unported-material: gains a property on a non-ported file   -> dropped (not ported)
	//   ported-async     : becomes Promise-returning on a ported file -> surfaced (separate theme)
	writeInv := func(version string, portedProps, asyncType string) {
		inv := map[string]any{
			"upstreamVersion": version,
			"interfaces": []map[string]any{
				decl("pkg:ai/.#PortedOptions", "@earendil-works/pi-ai", "dist/ported.d.ts", "PortedOptions", portedProps, ""),
				decl("pkg:ai/.#CosmeticOptions", "@earendil-works/pi-ai", "dist/ported.d.ts", "CosmeticOptions", "keep", ""),
				decl("pkg:ai/.#UnportedOptions", "@earendil-works/pi-ai", "dist/dark.d.ts", "UnportedOptions", portedProps, ""),
				decl("pkg:ai/.#PortedGetter", "@earendil-works/pi-ai", "dist/ported.d.ts", "PortedGetter", "", asyncType),
			},
		}
		data, _ := json.Marshal(inv)
		if err := os.WriteFile(filepath.Join(dir, "upstream-v"+version+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeInv(from, "keep", "() => string")
	writeInv(to, "keep,samplingParams", "() => Promise<string>")

	manifest := map[string]any{
		"from": from, "to": to,
		"changes": []map[string]any{
			{"id": "pkg:ai/.#PortedOptions", "change": "changed"},
			{"id": "pkg:ai/.#CosmeticOptions", "change": "changed"},
			{"id": "pkg:ai/.#UnportedOptions", "change": "changed"},
			{"id": "pkg:ai/.#PortedGetter", "change": "changed"},
			{"id": "pkg:ai/.#Added", "change": "added"},
		},
	}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, "delta-v"+from+"-v"+to+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Only ported.ts is ✅; dark.ts is ⬜.
	portMap := "" +
		"| upstream | go | status |\n" +
		"|---|---|---|\n" +
		"| `packages/ai/src/ported.ts` | `ai/ported.go` | ✅ |\n" +
		"| `packages/ai/src/dark.ts` | `ai/dark.go` | ⬜ |\n"
	if err := os.WriteFile(filepath.Join(root, "PORT_MAP.md"), []byte(portMap), 0o644); err != nil {
		t.Fatal(err)
	}

	response, err := Execute(context.Background(), Request{Operation: OperationDispositionPlan, Root: root})
	if err != nil {
		t.Fatalf("execute disposition-plan: %v", err)
	}
	var plan DispositionPlan
	if err := json.Unmarshal([]byte(response.Output), &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}

	if plan.From != from || plan.To != to {
		t.Fatalf("version range = %q->%q, want %q->%q", plan.From, plan.To, from, to)
	}
	if plan.TotalChanged != 4 {
		t.Fatalf("totalChanged = %d, want 4 (the added record is excluded)", plan.TotalChanged)
	}
	if plan.MaterialOnPorted != 2 {
		t.Fatalf("materialOnPorted = %d, want 2 (cosmetic and unported dropped)", plan.MaterialOnPorted)
	}
	if len(plan.Groups) != 2 {
		t.Fatalf("groups = %d, want 2 (props-added, sync-to-async)", len(plan.Groups))
	}

	byKind := map[string]DispositionGroup{}
	for _, g := range plan.Groups {
		byKind[g.ChangeKind] = g
	}
	added, ok := byKind["props-added"]
	if !ok {
		t.Fatal("missing props-added group")
	}
	if len(added.Interfaces) != 1 || added.Interfaces[0] != "PortedOptions" {
		t.Fatalf("props-added interfaces = %v, want [PortedOptions]", added.Interfaces)
	}
	if len(added.AddedProps) != 1 || added.AddedProps[0] != "samplingParams" {
		t.Fatalf("props-added addedProps = %v, want [samplingParams]", added.AddedProps)
	}
	if added.PortMapStatus != "✅" {
		t.Fatalf("props-added portMapStatus = %q, want ✅", added.PortMapStatus)
	}
	// The Porter never derives a verdict: recommendation/decision are empty slots.
	if added.Recommendation != "" || added.Decision != "" {
		t.Fatalf("Porter must not pre-fill recommendation/decision, got %q/%q", added.Recommendation, added.Decision)
	}
	async, ok := byKind["sync-to-async"]
	if !ok {
		t.Fatal("missing sync-to-async group")
	}
	if len(async.Interfaces) != 1 || async.Interfaces[0] != "PortedGetter" {
		t.Fatalf("sync-to-async interfaces = %v, want [PortedGetter]", async.Interfaces)
	}
}

// TestDispositionPlanRejectsExtraFields pins that disposition-plan is a
// root-only read: any closure/correspondence field is rejected.
func TestDispositionPlanRejectsExtraFields(t *testing.T) {
	for _, request := range []Request{
		{Operation: OperationDispositionPlan, Database: "closure.db"},
		{Operation: OperationDispositionPlan, RecordID: "x"},
		{Operation: OperationDispositionPlan, UpstreamVersion: "0.84.0"},
		{Operation: OperationDispositionPlan, Dataset: "port-map"},
	} {
		if err := request.Validate(); err == nil {
			t.Fatalf("expected rejection for %+v", request)
		}
	}
}

func decl(id, pkg, path, name, props, callType string) map[string]any {
	shape := map[string]any{"type": name}
	if callType != "" {
		shape["type"] = callType
	}
	properties := []map[string]any{}
	if props != "" {
		for _, n := range splitComma(props) {
			properties = append(properties, map[string]any{"name": n})
		}
	}
	shape["properties"] = properties
	return map[string]any{
		"id": id, "package": pkg, "name": name,
		"source": map[string]any{"path": "node_modules/" + pkg + "/" + path},
		"shape":  shape,
	}
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
