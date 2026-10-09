package main

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeKubectl answers the runner's kubectl calls from a script of responses and records every call.
type fakeKubectl struct {
	calls    [][]string
	stdin    [][]byte
	jobPolls int
	podLog   []byte
	fail     map[string]bool
	// phases, when set, are the pod phases of successive "get pods" calls; the last repeats.
	phases []string
	// failed makes the Job end Failed instead of Complete.
	failed bool
}

func (f *fakeKubectl) run(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	f.stdin = append(f.stdin, stdin)
	key := strings.Join(args, " ")
	for prefix := range f.fail {
		if strings.HasPrefix(key, prefix) {
			return nil, errors.New("forbidden")
		}
	}
	switch {
	case args[0] == "apply":
		return []byte("job.batch/x created\n"), nil
	case args[0] == "get" && args[1] == "pods":
		phase := "Running"
		if len(f.phases) > 0 {
			phase = f.phases[0]
			if len(f.phases) > 1 {
				f.phases = f.phases[1:]
			}
		}
		return []byte(`{"items":[{"metadata":{"name":"job-abc"},"spec":{"nodeName":"node-a"},"status":{"phase":"` + phase + `"}}]}`), nil
	case args[0] == "get" && args[1] == "job":
		f.jobPolls++
		if f.jobPolls < 2 {
			return []byte(`{"status":{"active":1}}`), nil
		}
		if f.failed {
			return []byte(`{"status":{"conditions":[{"type":"Failed","status":"True","reason":"BackoffLimitExceeded"}]}}`), nil
		}
		return []byte(`{"status":{"conditions":[{"type":"Complete","status":"True"}]}}`), nil
	case args[0] == "logs":
		return f.podLog, nil
	case args[0] == "get" && args[1] == "node":
		return []byte(`{"status":{"nodeInfo":{"kernelVersion":"6.1.0","osImage":"Linux","swap":{"capacity":0}},"capacity":{"cpu":"48"},"allocatable":{"cpu":"47"}}}`), nil
	case args[0] == "get" && args[1] == "--raw":
		return []byte(`{"kubeletconfig":{"cpuManagerPolicy":"static"}}`), nil
	case args[0] == "delete":
		return nil, nil
	}
	return nil, errors.New("unexpected kubectl call: " + key)
}

func TestRunJobAppliesWaitsCollectsAndDeletes(t *testing.T) {
	log, _ := os.ReadFile(filepath.Join("testdata", "compare.jsonl"))
	k := &fakeKubectl{podLog: log}
	var progress bytes.Buffer
	out, err := runJob(context.Background(), k, []byte("manifest"), "job1", time.Millisecond, time.Minute, false, &progress, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !out.succeeded || out.node != "node-a" || out.pod != "job-abc" || !bytes.Equal(out.log, log) {
		t.Errorf("outcome %+v", out)
	}
	if string(k.stdin[0]) != "manifest" || !slices.Equal(k.calls[0], []string{"apply", "-f", "-"}) {
		t.Errorf("apply: %v", k.calls[0])
	}
	if last := k.calls[len(k.calls)-1]; last[0] != "delete" || last[2] != "job1" {
		t.Errorf("the Job is deleted after collection: %v", last)
	}
	if !slices.ContainsFunc(k.calls, func(c []string) bool { return slices.Equal(c, []string{"logs", "pod/job-abc", "-c", "bench"}) }) {
		t.Errorf("logs not read from the pod: %v", k.calls)
	}

	k = &fakeKubectl{podLog: log}
	if _, err := runJob(context.Background(), k, []byte("m"), "job1", time.Millisecond, time.Minute, true, &progress, time.Now, nil); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(k.calls, func(c []string) bool { return c[0] == "delete" }) {
		t.Error("-keep must leave the Job")
	}
}

func TestClusterFactsDegradeWhenForbidden(t *testing.T) {
	k := &fakeKubectl{}
	facts := clusterFacts(context.Background(), k, "node-a")
	if facts.str("cpuManagerPolicy") != "static" {
		t.Errorf("configz policy: %s", facts.values["cpuManagerPolicy"])
	}
	var info map[string]string
	if !facts.get("nodeInfo", &info) || info["kernelVersion"] != "6.1.0" || info["cpuCapacity"] != "48" {
		t.Errorf("node info %v", info)
	}
	k = &fakeKubectl{fail: map[string]bool{"get node": true, "get --raw": true}}
	facts = clusterFacts(context.Background(), k, "node-a")
	if facts.str("cpuManagerPolicy") != "unreadable" || facts.str("nodeInfo") != "unreadable" {
		t.Errorf("a namespace-scoped account records unreadable facts: %v", facts.keys)
	}
}

func TestExecKubectlGlobalFlags(t *testing.T) {
	k := execKubectl{kubeconfig: "/cfg", context: "ctx", namespace: "ns"}
	if got := k.args([]string{"get", "pods"}); !slices.Equal(got, []string{"--kubeconfig", "/cfg", "--context", "ctx", "--namespace", "ns", "get", "pods"}) {
		t.Errorf("args %v", got)
	}
	if got := (execKubectl{}).args([]string{"apply"}); !slices.Equal(got, []string{"apply"}) {
		t.Errorf("without settings kubectl uses its own defaults ($KUBECONFIG): %v", got)
	}
}

// writeKubectl writes a kubectl stand-in that answers from files: the Job condition, the pod log, and whether node
// reads are forbidden, as for a namespace-scoped account.
func writeKubectl(t *testing.T, condition, podLog string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
while [ $# -gt 0 ]; do case $1 in --kubeconfig|--context|--namespace) shift 2 ;; *) break ;; esac; done
echo "$*" >> ` + calls + `
case "$1 $2" in
"apply -f") cat > ` + filepath.Join(dir, "applied.yaml") + ` ;;
"get job") echo '{"status":{"conditions":[{"type":"` + condition + `","status":"True","reason":"R"}]}}' ;;
"get pods") echo '{"items":[{"metadata":{"name":"pod-1"},"spec":{"nodeName":"node-a"},"status":{"phase":"Running"}}]}' ;;
"cp "*) cp "$2" ` + dir + `/copied-$(basename "${3#*:}") ;;
"logs pod/pod-1") cat ` + podLog + ` ;;
"delete job") ;;
*) echo "forbidden: $*" >&2; exit 1 ;;
esac
`
	path := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, dir
}

// The whole runner path against a kubectl stand-in: render, apply, wait, collect, judge, append the tracker line.
func TestCompareThroughKubectl(t *testing.T) {
	podLog, _ := filepath.Abs(filepath.Join("testdata", "compare.jsonl"))
	kubectlPath, dir := writeKubectl(t, "Complete", podLog)
	out := t.TempDir()
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"compare", "-values", os.DevNull, "-kubectl", kubectlPath, "-poll", "1ms", "-out", out, "-turns", "50", "-rounds", "6"}, &stdout, &stderr, func(k string) string { return env[k] })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	track, err := os.ReadFile(filepath.Join(out, "track.jsonl"))
	if err != nil || !strings.Contains(string(track), `"rule":"native"`) || !bytes.Equal(bytes.TrimSpace(track), bytes.TrimSpace(stdout.Bytes())) {
		t.Fatalf("tracker file %v:\n%s\nstdout:\n%s", err, track, stdout.String())
	}
	applied, _ := os.ReadFile(filepath.Join(dir, "applied.yaml"))
	if !bytes.Contains(applied, []byte(`namespace: "bench-ns"`)) {
		t.Errorf("applied manifest:\n%s", applied)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	for _, want := range []string{"apply -f -", "logs pod/pod-1 -c bench", "get node node-a -o json", "delete job"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("no %q call:\n%s", want, calls)
		}
	}
	runs, _ := filepath.Glob(filepath.Join(out, "k8sbench-cmp-*", "ENV.txt"))
	if len(runs) != 1 {
		t.Fatalf("run directories: %v", runs)
	}
	envText, _ := os.ReadFile(runs[0])
	if !strings.Contains(string(envText), "cpu manager policy configz unreadable; inferred from the pod's cpuset: none") || !strings.Contains(string(envText), "node info unreadable") {
		t.Errorf("forbidden node reads are recorded as unreadable:\n%s", envText)
	}
}

func TestFailedJobReportsThePodError(t *testing.T) {
	podLog := filepath.Join(t.TempDir(), "pod.log")
	if err := os.WriteFile(podLog, []byte(`{"t":"error","message":"the image was built from abc, not the requested commit def"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	kubectlPath, _ := writeKubectl(t, "Failed", podLog)
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"compare", "-values", os.DevNull, "-kubectl", kubectlPath, "-poll", "1ms", "-out", t.TempDir()}, &bytes.Buffer{}, &stderr, func(k string) string { return env[k] })
	if code != 1 || !strings.Contains(stderr.String(), "not the requested commit def") {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
}

// A bundle session against the kubectl stand-in: the runner copies the packed bundle and then READY into the running
// pod, the copy has the sha256 the Job carries, and the result records the bundle and its artifact digests.
func TestBundleSessionThroughKubectl(t *testing.T) {
	podLog, _ := filepath.Abs(filepath.Join("testdata", "compare.jsonl"))
	kubectlPath, dir := writeKubectl(t, "Complete", podLog)
	bundle := fakeBundle(t, elf.EM_X86_64)
	out := t.TempDir()
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_BUNDLE": bundle}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"compare", "-values", os.DevNull, "-kubectl", kubectlPath, "-poll", "1ms", "-out", out, "-turns", "50", "-rounds", "6"}, &stdout, &stderr, func(k string) string { return env[k] })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	text := string(calls)
	copyTar, copyReady, logs := strings.Index(text, "cp "), strings.Index(text, "pod-1:/bundle/READY -c bench"), strings.Index(text, "logs pod/pod-1")
	if copyTar < 0 || !strings.Contains(text, "pod-1:/bundle/bundle.tar.gz -c bench") || copyReady < copyTar || logs < copyReady {
		t.Errorf("want the bundle, then READY, then the logs:\n%s", text)
	}
	if ready, err := os.ReadFile(filepath.Join(dir, "copied-READY")); err != nil || len(ready) != 0 {
		t.Errorf("READY is an empty marker: %v, %d bytes", err, len(ready))
	}
	applied, _ := os.ReadFile(filepath.Join(dir, "applied.yaml"))
	sum, _ := fileSHA256(filepath.Join(dir, "copied-bundle.tar.gz"))
	if !bytes.Contains(applied, []byte(`"K8SBENCH_BUNDLE_SHA256"`)) || !bytes.Contains(applied, []byte(`"`+sum+`"`)) {
		t.Errorf("the Job carries the sha256 of the bytes copied (%s):\n%s", sum, applied)
	}
	runs, _ := filepath.Glob(filepath.Join(out, "k8sbench-cmp-*", "cluster.json"))
	if len(runs) != 1 {
		t.Fatalf("run directories: %v", runs)
	}
	var cluster struct {
		Delivery, Image, BundleSha256, BundleCommit string
		BundleArtifacts                             map[string]string
	}
	data, _ := os.ReadFile(runs[0])
	if err := json.Unmarshal(data, &cluster); err != nil {
		t.Fatal(err)
	}
	wasm, _ := fileSHA256(filepath.Join(bundle, "dist", "core-tinygo.wasm"))
	if cluster.Delivery != deliveryBundle || cluster.Image != defaultBaseImage || cluster.BundleSha256 != sum || cluster.BundleArtifacts["dist/core-tinygo.wasm"] != wasm || len(cluster.BundleArtifacts) != 6 {
		t.Errorf("cluster.json: %s", data)
	}
}

// A pod that fails its own check (wrong architecture) stops while the runner copies: the runner then collects the
// pod's log instead of reporting the copy error, and a copy into a pod that still runs is an error.
func TestDeliveryIntoAStoppedPod(t *testing.T) {
	k := &fakeKubectl{podLog: []byte(`{"t":"error","message":"the bundle is linux/arm64 but the node is linux/amd64"}`), fail: map[string]bool{"cp": true}, phases: []string{"Running", "Failed"}, failed: true}
	copyErr := func(ctx context.Context, pod string) error {
		_, err := k.run(ctx, nil, "cp", "x", pod+":/bundle/bundle.tar.gz")
		return err
	}
	out, err := runJob(context.Background(), k, []byte("m"), "job1", time.Millisecond, time.Minute, false, &bytes.Buffer{}, time.Now, copyErr)
	if err != nil || out.succeeded || !bytes.Contains(out.log, []byte("linux/arm64")) {
		t.Fatalf("want the failed pod's log, got %v %+v", err, out)
	}
	k = &fakeKubectl{fail: map[string]bool{"cp": true}}
	if _, err := runJob(context.Background(), k, []byte("m"), "job1", time.Millisecond, time.Minute, false, &bytes.Buffer{}, time.Now, copyErr); err == nil || !strings.Contains(err.Error(), "copy the bundle") {
		t.Fatalf("a failed copy into a running pod is an error: %v", err)
	}
}
