package pigletbuild

import "testing"

func TestBuildPlanAutomaticallyFusesCompatibleGo(t *testing.T) {
	host := Target{OS: "darwin", Arch: "arm64"}
	plan := BuildPlan([]ExtensionInput{{Name: "go-core", Language: Go, Fusible: true}}, Options{
		Targets: []Target{host}, Sandbox: Sandbox{Native: host},
	})
	if len(plan.Ext) != 1 || plan.Ext[0].Decision != DecisionFuse || plan.Ext[0].Compat[host.String()] != CompatNative {
		t.Fatalf("plan = %#v", plan)
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
}

func TestBuildPlanUsesSubprocessForNonFusibleComponents(t *testing.T) {
	host := Target{OS: "darwin", Arch: "arm64"}
	linux := Target{OS: "linux", Arch: "amd64"}
	plan := BuildPlan([]ExtensionInput{
		{Name: "isolated-go", Language: Go, NotFusibleReason: "isolation-required"},
		{Name: "rust", Language: Rust, NotFusibleReason: "language:rust"},
		{Name: "python", Language: Python, NotFusibleReason: "language:python"},
	}, Options{Targets: []Target{host, linux}, Sandbox: Sandbox{Native: host}})
	if len(plan.Ext) != 3 {
		t.Fatalf("plan = %#v", plan)
	}
	for _, entry := range plan.Ext {
		if entry.Decision != DecisionSidecar || entry.Reason == "" {
			t.Fatalf("entry = %#v", entry)
		}
	}
	if plan.Ext[0].Compat[linux.String()] != CompatNative {
		t.Fatalf("Go compatibility = %#v", plan.Ext[0].Compat)
	}
	if plan.Ext[1].Compat[linux.String()] != CompatHostReliant || plan.Ext[2].Compat[linux.String()] != CompatHostReliant {
		t.Fatalf("cross-target compatibility = %#v", plan.Ext)
	}
	if len(plan.Warnings) != 2 {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
}

func TestBuildPlanUsesBuilderCrossToolchains(t *testing.T) {
	host := Target{OS: "darwin", Arch: "arm64"}
	linux := Target{OS: "linux", Arch: "amd64"}
	plan := BuildPlan([]ExtensionInput{
		{Name: "rust", Language: Rust, NotFusibleReason: "language:rust"},
		{Name: "python", Language: Python, NotFusibleReason: "language:python"},
	}, Options{Targets: []Target{linux}, Sandbox: Sandbox{Native: host, RustCross: true, PyCross: true}})
	if len(plan.Warnings) != 0 {
		t.Fatalf("warnings = %#v", plan.Warnings)
	}
	for _, entry := range plan.Ext {
		if entry.Compat[linux.String()] != CompatNative {
			t.Fatalf("entry = %#v", entry)
		}
	}
}

func TestValidateRejectsUnresolvedAndEmptyPiglets(t *testing.T) {
	host := Target{OS: "darwin", Arch: "arm64"}
	plan := BuildPlan([]ExtensionInput{{Name: "go", Language: Go, Fusible: true}}, Options{
		Targets: []Target{host}, Sandbox: Sandbox{Native: host},
	})
	if verdict := Validate(plan, nil, 1, false); !verdict.OK {
		t.Fatalf("verdict = %#v", verdict)
	}
	if verdict := Validate(plan, []string{"missing"}, 1, false); verdict.OK || len(verdict.Blockers) != 1 {
		t.Fatalf("unresolved verdict = %#v", verdict)
	}
	if verdict := Validate(BuildPlan(nil, Options{Targets: []Target{host}, Sandbox: Sandbox{Native: host}}), nil, 0, false); verdict.OK {
		t.Fatalf("empty verdict = %#v", verdict)
	}
}

func TestValidateRejectsNonFusedExtensionWhenRequired(t *testing.T) {
	host := Target{OS: "linux", Arch: "amd64"}
	plan := BuildPlan([]ExtensionInput{
		{Name: "login", Language: Go, Fusible: true},
		{Name: "runner", Language: Python, NotFusibleReason: "language:python"},
	}, Options{Targets: []Target{host}, Sandbox: Sandbox{Native: host}})
	verdict := Validate(plan, nil, 2, true)
	if verdict.OK || len(verdict.Blockers) != 1 {
		t.Fatalf("verdict = %#v", verdict)
	}
	want := `extension "runner" resolves to sidecar: language:python; build.extensionRealization requires fused`
	if verdict.Blockers[0] != want {
		t.Fatalf("blocker = %q, want %q", verdict.Blockers[0], want)
	}
}
