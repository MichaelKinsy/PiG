package main

import (
	"bytes"
	"context"
	"debug/elf"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A profile whose programs the bundle does not carry is refused before any Job runs, naming what is missing; the
// base image's node needs no artifact.
func TestBundleMustCarryTheProfilesPrograms(t *testing.T) {
	dir := fakeBundle(t, elf.EM_X86_64)
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_BUNDLE": dir}
	session := func(mode, profile string) (int, string) {
		var stderr bytes.Buffer
		code := run(context.Background(), []string{mode, "-dry-run", "-values", os.DevNull, "-kubectl", "/nonexistent/kubectl", "-profile", profile, "-turns", "50", "-runs", "6", "-node", "node-a"}, &bytes.Buffer{}, &stderr, func(k string) string { return env[k] })
		return code, stderr.String()
	}
	if code, out := session("compare", "durable-native"); code != 0 {
		t.Fatalf("durable-native runs from the bundle: %d %s", code, out)
	}
	code, out := session("compare", "durable-core")
	for _, want := range []string{"cannot run profile durable-core", "pig-tinygo needs core/bench-pig.mjs", "pig-native needs bin/sqlhost-bench", "pi needs core/bench-pi.mjs"} {
		if code != 1 || !strings.Contains(out, want) {
			t.Errorf("want %q: exit %d: %s", want, code, out)
		}
	}
	// A calibration runs only the reference, so only its program must be there.
	code, out = session("calibrate", "durable-core")
	if code != 1 || !strings.Contains(out, "pi needs core/bench-pi.mjs") || strings.Contains(out, "sqlhost-bench") {
		t.Errorf("calibrate: exit %d: %s", code, out)
	}

	// With the programs listed, the same sessions pass; an unlisted file does not count.
	if err := os.MkdirAll(filepath.Join(dir, "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeELF(t, filepath.Join(dir, "bin", "sqlhost-bench"), elf.EM_X86_64)
	for _, f := range []string{"core/bench-pig.mjs", "core/bench-pi.mjs"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("// program\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if code, out := session("compare", "durable-core"); code != 1 || !strings.Contains(out, "pi needs core/bench-pi.mjs") {
		t.Errorf("unlisted programs: exit %d: %s", code, out)
	}
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	if code, out := session("compare", "durable-core"); code != 0 {
		t.Errorf("listed programs: exit %d: %s", code, out)
	}
}

// The pod fills {root} in anywhere in an argument, so a {root}/ path inside an argument needs its artifact too; a
// {root}/ directory is there when the bundle lists a file in it.
func TestBundleProgramsInsideArguments(t *testing.T) {
	b, err := inspectBundle(fakeBundle(t, elf.EM_X86_64))
	if err != nil {
		t.Fatal(err)
	}
	prof, err := loadProfile("durable-native")
	if err != nil {
		t.Fatal(err)
	}
	pig, _ := prof.byName("pig")
	check := func(sample ...string) error {
		p := *prof
		tg := pig
		tg.Sample = sample
		p.Targets = []target{tg}
		return checkBundlePrograms(&p, []string{"pig"}, b)
	}
	for _, ok := range [][]string{
		{"durableperf", "sample", "-wasm={root}/dist/core-go.wasm"},
		{"durableperf", "sample", "-path", "{root}/dist/core-go.wasm:{root}/dist/core-tinygo.wasm"},
		{"durableperf", "sample", "-dir", "{root}/dist", "-dir2", "{root}/dist/"},
		{"{root}/bin/durableperf", "sample"},
		{"durableperf", "sample", "-root", "{root}/"},
	} {
		if err := check(ok...); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for want, bad := range map[string][]string{
		"pig needs dist/missing.wasm": {"durableperf", "sample", "-wasm={root}/dist/missing.wasm"},
		"pig needs core/b.wasm":       {"durableperf", "sample", "-path", "{root}/dist/core-go.wasm:{root}/core/b.wasm"},
		"pig needs core":              {"durableperf", "sample", "-dir", "{root}/core"},
		"pig needs bin/other-bench":   {"{root}/bin/other-bench", "sample"},
		"pig needs bin/durableperf-x": {"durableperf-x", "sample"},
	} {
		if err := check(bad...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: want %q: %v", bad, want, err)
		}
	}
}

// Every listed program under bin/ must be a Linux executable of the bundle's architecture, not only the ones the
// runner knows by name.
func TestEveryBundleBinaryHasTheBundlesArchitecture(t *testing.T) {
	dir := fakeBundle(t, elf.EM_X86_64)
	writeELF(t, filepath.Join(dir, "bin", "other-bench"), elf.EM_AARCH64)
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(dir); err == nil || !strings.Contains(err.Error(), "bin/other-bench") {
		t.Errorf("a binary of another architecture: %v", err)
	}
}
