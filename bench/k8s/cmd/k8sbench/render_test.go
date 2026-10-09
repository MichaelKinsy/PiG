package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const testImage = "registry.invalid/bench@sha256:0000000000000000000000000000000000000000000000000000000000000000"

func testValues() values {
	v := defaultValues()
	v.Namespace = "bench-ns"
	v.Image = testImage
	return v
}

func testPlan(t *testing.T, v values, mode string) *plan {
	t.Helper()
	prof, err := loadProfile("durable-native")
	if err != nil {
		t.Fatal(err)
	}
	return &plan{Run: runID(mode, "0123456789abcdef", time.Date(2026, 10, 9, 1, 2, 3, 0, time.UTC)), Mode: mode, Profile: prof,
		Targets: sessionTargets(prof, mode), Turns: []int{3500}, Rounds: 12, CPUs: v.CPUs, Pin: "auto", StepTimeoutSeconds: 3600}
}

func renderDoc(t *testing.T, v values, mode string) (map[string]any, []byte) {
	t.Helper()
	out, err := renderJob(v, testPlan(t, v, mode), bundleRef{})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("rendered manifest is not YAML: %v\n%s", err, out)
	}
	return doc, out
}

// at walks a decoded YAML document by map keys and list indexes.
func at(t *testing.T, doc any, path ...any) any {
	t.Helper()
	cur := doc
	for _, p := range path {
		switch key := p.(type) {
		case string:
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("%v: not a map at %q", path, key)
			}
			cur = m[key]
		case int:
			l, ok := cur.([]any)
			if !ok || key >= len(l) {
				t.Fatalf("%v: no index %d", path, key)
			}
			cur = l[key]
		}
	}
	return cur
}

func podSpec(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	return at(t, doc, "spec", "template", "spec").(map[string]any)
}

// A Guaranteed pod needs every container's requests equal to its limits for both CPU and memory, and the static
// CPU manager gives exclusive cores only to an integer CPU request.
func TestRenderGuaranteedQoS(t *testing.T) {
	for _, cpus := range []int{1, 2, 8} {
		v := testValues()
		v.CPUs, v.Memory = cpus, "12Gi"
		doc, _ := renderDoc(t, v, modeCompare)
		spec := podSpec(t, doc)
		if _, ok := spec["initContainers"]; ok {
			t.Fatal("an init container without equal requests and limits would demote the pod from Guaranteed")
		}
		containers := spec["containers"].([]any)
		if len(containers) != 1 {
			t.Fatalf("want one container, got %d", len(containers))
		}
		res := at(t, containers[0], "resources").(map[string]any)
		req, lim := res["requests"].(map[string]any), res["limits"].(map[string]any)
		for _, k := range []string{"cpu", "memory"} {
			if req[k] == nil || req[k] != lim[k] {
				t.Errorf("cpus=%d: %s request %v != limit %v", cpus, k, req[k], lim[k])
			}
		}
		if req["cpu"] != strconv.Itoa(cpus) {
			t.Errorf("cpu request %v, want the integer %d", req["cpu"], cpus)
		}
		if req["memory"] != "12Gi" {
			t.Errorf("memory request %v", req["memory"])
		}
		if at(t, doc, "spec", "backoffLimit") != 0 || spec["restartPolicy"] != "Never" {
			t.Error("a failed measurement must not be retried into the same results")
		}
	}
}

func newFlagSet() *flag.FlagSet { return flag.NewFlagSet("test", flag.ContinueOnError) }

func TestRenderPinningAndPool(t *testing.T) {
	v := testValues()
	v.Node = "worker-7"
	v.NodeSelector = map[string]string{"pool": "bench"}
	seconds := int64(30)
	v.Tolerations = []toleration{{Key: "dedicated", Operator: "Equal", Value: "bench", Effect: "NoSchedule"}, {Key: "x", Operator: "Exists", Effect: "NoExecute", TolerationSeconds: &seconds}}
	v.PVC = "bench-results"
	v.ImagePullSecrets = []string{"regcred"}
	doc, _ := renderDoc(t, v, modeCompare)
	spec := podSpec(t, doc)
	term := at(t, spec, "affinity", "nodeAffinity", "requiredDuringSchedulingIgnoredDuringExecution", "nodeSelectorTerms", 0, "matchFields", 0).(map[string]any)
	if term["key"] != "metadata.name" || term["operator"] != "In" || !slices.Equal(term["values"].([]any), []any{"worker-7"}) {
		t.Errorf("node pin: %v", term)
	}
	if !mapEqual(spec["nodeSelector"], map[string]any{"pool": "bench"}) {
		t.Errorf("nodeSelector: %v", spec["nodeSelector"])
	}
	tol := spec["tolerations"].([]any)
	if len(tol) != 2 || !mapEqual(tol[0], map[string]any{"key": "dedicated", "operator": "Equal", "value": "bench", "effect": "NoSchedule"}) || at(t, tol[1], "tolerationSeconds") != 30 {
		t.Errorf("tolerations: %v", tol)
	}
	if at(t, spec, "volumes", 1, "persistentVolumeClaim", "claimName") != "bench-results" || at(t, spec, "containers", 0, "volumeMounts", 1, "mountPath") != "/results" {
		t.Error("PVC not mounted at /results")
	}
	if at(t, spec, "imagePullSecrets", 0, "name") != "regcred" {
		t.Error("imagePullSecrets")
	}

	// Without the optional values none of their keys appear.
	doc, out := renderDoc(t, testValues(), modeCompare)
	spec = podSpec(t, doc)
	for _, k := range []string{"affinity", "nodeSelector", "tolerations", "imagePullSecrets", "serviceAccountName"} {
		if _, ok := spec[k]; ok {
			t.Errorf("unset %s rendered", k)
		}
	}
	if bytes.Contains(out, []byte("persistentVolumeClaim")) {
		t.Error("unset pvc rendered")
	}
}

func mapEqual(a any, b map[string]any) bool {
	m, ok := a.(map[string]any)
	if !ok || len(m) != len(b) {
		return false
	}
	for k, v := range b {
		if m[k] != v {
			return false
		}
	}
	return true
}

// The manifest holds no cluster-specific value of its own: every string is a template constant, one of the renderer's
// fixed values, or a configured value, so nothing about one cluster can leak from the template into another. The
// renderer's fixed values in bundle mode are public: the pinned official Node image and the pod script.
func TestRenderHasOnlyConfiguredValues(t *testing.T) {
	for _, delivery := range []string{deliveryImage, deliveryBundle} {
		v := testValues()
		v.Namespace, v.Node, v.PVC = "sentinel-ns", "sentinel-node", "sentinel-pvc"
		v.Image = "sentinel.invalid/img@sha256:" + strings.Repeat("a", 64)
		bundleSum := ""
		if delivery == deliveryBundle {
			v.Image, v.Bundle, bundleSum = "", "/sentinel/bundle", strings.Repeat("b", 64)
		}
		v.NodeSelector = map[string]string{"sentinel-key": "sentinel-value"}
		v.Tolerations = []toleration{{Key: "sentinel-taint", Operator: "Exists"}}
		v.ServiceAccountName = "sentinel-sa"
		pl := testPlan(t, v, modeCompare)
		out, err := renderJob(v, pl, bundleRef{sha256: bundleSum, arch: "amd64"})
		if err != nil {
			t.Fatal(err)
		}
		var doc any
		if err := yaml.Unmarshal(out, &doc); err != nil {
			t.Fatal(err)
		}
		encodedPlan, _ := marshalPlain(pl)
		configured := map[string]bool{v.Namespace: true, v.Image: true, v.Node: true, v.PVC: true, "sentinel-key": true, "sentinel-value": true,
			"sentinel-taint": true, "Exists": true, "sentinel-sa": true, pl.Run: true, string(encodedPlan): true, "2": true, v.Memory: true, v.ScratchSize: true,
			modeCompare: true, pl.Profile.Name: true, bundleSum: true}
		constants := map[string]bool{}
		for _, s := range regexp.MustCompile(`[A-Za-z0-9_./-]+`).FindAllString(jobTemplate, -1) {
			constants[strings.Trim(s, `"`)] = true
		}
		for k, val := range jobLabels(pl) {
			constants[k], constants[val] = true, true
		}
		for _, c := range []string{"/opt/pig/bin/k8sbench", "pod", "K8SBENCH_DELIVERY", "K8SBENCH_IMAGE_REF", "K8SBENCH_BUNDLE_SHA256", "K8SBENCH_BUNDLE_ARCH", "amd64", "K8SBENCH_ROOT", "PATH",
			deliveryImage, deliveryBundle, defaultBaseImage, "sh", "-c", podBundleScript, bundleDir + "/pig",
			bundleDir + "/pig/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"} {
			constants[c] = true
		}
		var walk func(any)
		walk = func(n any) {
			switch x := n.(type) {
			case map[string]any:
				for k, val := range x {
					if !constants[k] && !configured[k] {
						t.Errorf("%s: key %q is neither a template constant nor a configured value", delivery, k)
					}
					walk(val)
				}
			case []any:
				for _, e := range x {
					walk(e)
				}
			case string:
				if !constants[x] && !configured[x] {
					t.Errorf("%s: value %q is neither a template constant nor a configured value", delivery, x)
				}
			}
		}
		walk(doc)
	}
}

// The committed files under bench/k8s, its user guide and its changelog entry carry placeholders, not a cluster: no
// address, no host name outside the public download sites the image build pins and Kubernetes API groups, no
// kubeconfig path.
func TestCommittedFilesHaveNoClusterValues(t *testing.T) {
	root := filepath.Join("..", "..")
	allowedHosts := map[string]bool{"nodejs.org": true, "github.com": true, "registry.npmjs.org": true, "app.kubernetes.io": true, "values.local": true, "rbac.authorization.k8s.io": true}
	host := regexp.MustCompile(`\b[a-z0-9-]+(\.[a-z0-9-]+)*\.(com|net|org|io|dev|cloud|local|internal|corp|lan)\b`)
	ipv4 := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	kubePath := regexp.MustCompile(`\.kube/[A-Za-z0-9]`)
	resolved := regexp.MustCompile(`"resolved": "https://([^/"]+)/`)
	scan := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		if filepath.Base(path) == "package-lock.json" {
			// A lockfile names package funding sites; what matters is where packages come from.
			for _, m := range resolved.FindAllStringSubmatch(text, -1) {
				if m[1] != "registry.npmjs.org" {
					t.Errorf("%s resolves a package from %q, not the public npm registry", path, m[1])
				}
			}
			return nil
		}
		for _, h := range host.FindAllString(text, -1) {
			if !allowedHosts[h] && !strings.HasSuffix(h, ".invalid") {
				t.Errorf("%s names host %q", path, h)
			}
		}
		for _, ip := range ipv4.FindAllString(text, -1) {
			t.Errorf("%s holds address %q", path, ip)
		}
		if kubePath.MatchString(text) {
			t.Errorf("%s names a kubeconfig path", path)
		}
		return nil
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "out" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".local.yaml") {
			return nil
		}
		return scan(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "..", "..")
	for _, path := range []string{filepath.Join(repo, "docs", "site", "docs", "benchmarking.md"), filepath.Join(repo, "changelog.d", "k8s-bench.md")} {
		if err := scan(path); err != nil {
			t.Fatal(err)
		}
	}
	gitignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil || !strings.Contains(string(gitignore), "values.local.yaml") {
		t.Error("bench/k8s/.gitignore must ignore values.local.yaml")
	}
	dockerignore, err := os.ReadFile(filepath.Join(root, "image", "Dockerfile.dockerignore"))
	if err != nil || !strings.Contains(string(dockerignore), "bench/k8s/values.local.yaml") {
		t.Error("the image build context must leave out values.local.yaml")
	}
}

func TestValuesExampleNeedsEveryPlaceholderReplaced(t *testing.T) {
	path := filepath.Join("..", "..", "values.example.yaml")
	noEnv := func(string) string { return "" }
	_, err := resolveValues(path, func(*values) error { return nil }, noEnv)
	if err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("the example's placeholders must be refused, got %v", err)
	}
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	v, err := resolveValues(path, func(*values) error { return nil }, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("example plus environment: %v", err)
	}
	if v.CPUs != 2 || v.Memory != "8Gi" || v.TTLSeconds != 604800 || v.DeadlineSeconds != 21600 {
		t.Errorf("example values not read: %+v", v)
	}
}

func TestValuesPrecedence(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "v.yaml")
	if err := os.WriteFile(file, []byte("namespace: from-file\nimage: "+testImage+"\ncpus: 4\nnode: file-node\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"K8SBENCH_NAMESPACE": "from-env", "K8SBENCH_CPUS": "3"}
	fs := newFlagSet()
	apply := valueFlags(fs)
	if err := fs.Parse([]string{"-cpus", "6"}); err != nil {
		t.Fatal(err)
	}
	v, err := resolveValues(file, apply, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if v.Namespace != "from-env" || v.CPUs != 6 || v.Node != "file-node" {
		t.Errorf("precedence file < env < flag broken: %+v", v)
	}
	if err := os.WriteFile(file, []byte("namespace: x\nunknownField: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveValues(file, func(*values) error { return nil }, func(string) string { return "" }); err == nil {
		t.Error("an unknown values field must be refused")
	}
}

func TestValidateRefusesUnsafeValues(t *testing.T) {
	cases := map[string]func(*values){
		"unpinned image":    func(v *values) { v.Image = "registry.invalid/bench:latest" },
		"fractional cpus":   func(v *values) { v.CPUs = 0 },
		"bad memory":        func(v *values) { v.Memory = "lots" },
		"bad namespace":     func(v *values) { v.Namespace = "Bench_NS" },
		"bad toleration":    func(v *values) { v.Tolerations = []toleration{{Operator: "Maybe"}} },
		"selector injected": func(v *values) { v.NodeSelector = map[string]string{"a": "b\nc: d"} },
	}
	for name, mutate := range cases {
		v := testValues()
		mutate(&v)
		if err := v.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	v := testValues()
	v.Image = "registry.invalid/bench:latest"
	v.AllowUnpinnedImage = true
	if err := v.validate(); err != nil {
		t.Errorf("allowUnpinnedImage: %v", err)
	}
}

func TestRenderCalibrationRunsTheReferenceAlone(t *testing.T) {
	v := testValues()
	pl := testPlan(t, v, modeCalibrate)
	if !slices.Equal(pl.Targets, []string{"pi"}) {
		t.Fatalf("calibration targets %v", pl.Targets)
	}
	if !dnsLabel.MatchString(pl.Run) || len(pl.Run) > 63 {
		t.Errorf("run id %q is not a usable Job name", pl.Run)
	}
	cmp := testPlan(t, v, modeCompare)
	if !slices.Equal(cmp.Targets, []string{"pig", "pi"}) {
		t.Fatalf("compare targets %v", cmp.Targets)
	}
	v.CPUs = 3
	if _, err := renderJob(v, pl, bundleRef{}); err == nil {
		t.Error("a plan pinning fewer cores than the pod's CPUs must be refused")
	}
}

// The dry run prints the manifest and never runs kubectl.
func TestDryRunTouchesNoCluster(t *testing.T) {
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	for _, args := range [][]string{
		{"render", "-values", os.DevNull, "-kubectl", "/nonexistent/kubectl"},
		{"compare", "-dry-run", "-values", os.DevNull, "-kubectl", "/nonexistent/kubectl", "-node", "worker-1"},
		{"calibrate", "-dry-run", "-values", os.DevNull, "-kubectl", "/nonexistent/kubectl", "-runs", "20"},
	} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), args, &stdout, &stderr, func(k string) string { return env[k] })
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, stderr.String())
		}
		var doc map[string]any
		if err := yaml.Unmarshal(stdout.Bytes(), &doc); err != nil || doc["kind"] != "Job" {
			t.Fatalf("%v: no Job manifest on stdout: %v\n%s", args, err, stdout.String())
		}
	}
}

// A session that cannot pass the noise gate is refused before it is submitted, not after hours on the cluster, and
// no -max-cv lets every noise floor through.
func TestSessionRefusesSettingsTheNoiseGateCannotPass(t *testing.T) {
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	for _, args := range [][]string{
		{"compare", "-dry-run", "-rounds", "2"},
		{"calibrate", "-dry-run", "-runs", "1"},
		{"compare", "-dry-run", "-max-cv", "NaN"},
		{"compare", "-dry-run", "-max-cv", "0"},
		{"calibrate", "-dry-run", "-max-cv", "+Inf"},
	} {
		args = append(args, "-values", os.DevNull, "-kubectl", "/nonexistent/kubectl")
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr, func(k string) string { return env[k] }); code != 1 || stdout.Len() != 0 {
			t.Errorf("%v: exit %d with %d bytes of manifest; want exit 1 and none", args, code, stdout.Len())
		}
	}
	// report applies the same gate: a NaN threshold must not pass a comparison.
	_, dir := setupRun(t, "compare.jsonl")
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"report", "-run", dir, "-max-cv", "NaN"}, &bytes.Buffer{}, &stderr, env2getenv(env)); code != 1 {
		t.Errorf("report -max-cv NaN: exit %d, want 1: %s", code, stderr.String())
	}
	if code := run(context.Background(), []string{"report", "-run", dir}, &bytes.Buffer{}, &stderr, env2getenv(env)); code != 0 {
		t.Errorf("report: exit %d: %s", code, stderr.String())
	}
}

func env2getenv(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}
