package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseQuantity(t *testing.T) {
	for in, want := range map[string]float64{
		"48": 48, "47500m": 47.5, "0.5": 0.5, "100m": 0.1, "1e3": 1000,
		"380Gi": 380 << 30, "395000000Ki": 395000000 << 10, "512M": 512e6, "2k": 2000, "1Ti": 1 << 40,
	} {
		got, err := parseQuantity(in)
		if err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "Gi", "1Xi", "1.2.3", "ten"} {
		if _, err := parseQuantity(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func podOf(t *testing.T, text string) k8sPod {
	t.Helper()
	var p k8sPod
	if err := jsonUnmarshal(text, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A pod reserves its containers' summed requests or, when larger, its largest init container's, plus its overhead.
func TestPodRequests(t *testing.T) {
	p := podOf(t, `{"spec":{"containers":[{"resources":{"requests":{"cpu":"500m","memory":"1Gi"}}},{"resources":{"requests":{"cpu":"1"}}}],
		"initContainers":[{"resources":{"requests":{"cpu":"2","memory":"512Mi"}}}],"overhead":{"cpu":"250m"}}}`)
	if got := podRequests(p, "cpu"); got != 2.25 {
		t.Errorf("cpu %v", got)
	}
	if got := podRequests(p, "memory"); got != 1<<30 {
		t.Errorf("memory %v", got)
	}
	if got := podRequests(k8sPod{}, "cpu"); got != 0 {
		t.Errorf("a pod without requests reserves nothing: %v", got)
	}

	// Sidecars (init containers with restartPolicy Always) run beside the app containers, so their requests add to
	// the sum, and a later init container runs beside the sidecars started before it.
	p = podOf(t, `{"spec":{"containers":[{"resources":{"requests":{"cpu":"1"}}}],
		"initContainers":[{"restartPolicy":"Always","resources":{"requests":{"cpu":"500m"}}},{"resources":{"requests":{"cpu":"2"}}}]}}`)
	if got := podRequests(p, "cpu"); got != 2.5 {
		t.Errorf("sidecar then init: cpu %v, want max(1+0.5, 2+0.5)", got)
	}
	p = podOf(t, `{"spec":{"containers":[{"resources":{"requests":{"cpu":"1"}}}],
		"initContainers":[{"restartPolicy":"Always","resources":{"requests":{"cpu":"1"}}},{"resources":{"requests":{"cpu":"500m"}}}]}}`)
	if got := podRequests(p, "cpu"); got != 2 {
		t.Errorf("sidecar beside the app: cpu %v, want 1+1", got)
	}
	// Pod-level requests replace the containers' aggregate for the resources they set.
	p = podOf(t, `{"spec":{"resources":{"requests":{"cpu":"3"}},"containers":[{"resources":{"requests":{"cpu":"1","memory":"1Gi"}}}],"overhead":{"cpu":"250m"}}}`)
	if got := podRequests(p, "cpu"); got != 3.25 {
		t.Errorf("pod-level cpu %v", got)
	}
	if got := podRequests(p, "memory"); got != 1<<30 {
		t.Errorf("memory without a pod-level request %v", got)
	}
}

func TestTolerates(t *testing.T) {
	ts := []toleration{{Key: "dedicated", Operator: "Equal", Value: "bench", Effect: "NoSchedule"}, {Key: "gpu", Operator: "Exists"}}
	for _, c := range []struct {
		key, value, effect string
		want               bool
	}{
		{"dedicated", "bench", "NoSchedule", true},
		{"dedicated", "bench", "NoExecute", false},
		{"dedicated", "other", "NoSchedule", false},
		{"gpu", "any", "NoExecute", true},
		{"other", "", "NoSchedule", false},
	} {
		if got := tolerates(ts, c.key, c.value, c.effect); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
	if !tolerates([]toleration{{Operator: "Exists"}}, "anything", "x", "NoSchedule") {
		t.Error("an empty key with Exists tolerates every taint")
	}
}

// The test cluster: four nodes that can take a 2-CPU, 8 GiB pod with different room, and one each that is not Ready,
// cordoned, outside the pool, tainted, or full.
const testNodes = `{"items":[
 {"metadata":{"name":"busy","labels":{"pool":"bench"}},"status":{"allocatable":{"cpu":"48","memory":"380Gi"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"idle","labels":{"pool":"bench"}},"status":{"allocatable":{"cpu":"48","memory":"380Gi"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"half","labels":{"pool":"bench"}},"status":{"allocatable":{"cpu":"47500m","memory":"380Gi"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"tainted-ok","labels":{"pool":"bench"}},"spec":{"taints":[{"key":"dedicated","value":"bench","effect":"NoSchedule"},{"key":"note","effect":"PreferNoSchedule"}]},"status":{"allocatable":{"cpu":"48","memory":"380Gi"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"down","labels":{"pool":"bench"}},"status":{"allocatable":{"cpu":"48","memory":"380Gi"},"conditions":[{"type":"Ready","status":"False"}]}},
 {"metadata":{"name":"cordoned","labels":{"pool":"bench"}},"spec":{"unschedulable":true},"status":{"allocatable":{"cpu":"48","memory":"380Gi"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"other-pool","labels":{"pool":"web"}},"status":{"allocatable":{"cpu":"64","memory":"380Gi"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"gpu","labels":{"pool":"bench"}},"spec":{"taints":[{"key":"gpu","effect":"NoExecute"}]},"status":{"allocatable":{"cpu":"64","memory":"380Gi"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"full","labels":{"pool":"bench"}},"status":{"allocatable":{"cpu":"48","memory":"380Gi"},"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"no-memory","labels":{"pool":"bench"}},"status":{"allocatable":{"cpu":"48","memory":"6Gi"},"conditions":[{"type":"Ready","status":"True"}]}}
]}`

const testPods = `{"items":[
 {"spec":{"nodeName":"busy","containers":[{"resources":{"requests":{"cpu":"40"}}}]},"status":{"phase":"Running"}},
 {"spec":{"nodeName":"half","containers":[{"resources":{"requests":{"cpu":"20","memory":"10Gi"}}}]},"status":{"phase":"Running"}},
 {"spec":{"nodeName":"half","containers":[{"resources":{"requests":{"cpu":"40"}}}]},"status":{"phase":"Succeeded"}},
 {"spec":{"nodeName":"tainted-ok","containers":[{"resources":{"requests":{"cpu":"10"}}}]},"status":{"phase":"Pending"}},
 {"spec":{"nodeName":"full","containers":[{"resources":{"requests":{"cpu":"46500m"}}}]},"status":{"phase":"Running"}},
 {"spec":{"nodeName":"idle"},"status":{"phase":"Running"}},
 {"spec":{"containers":[{"resources":{"requests":{"cpu":"48"}}}]},"status":{"phase":"Pending"}}
]}`

func testRoomValues() values {
	return values{CPUs: 2, Memory: "8Gi", NodeSelector: map[string]string{"pool": "bench"}, Tolerations: []toleration{{Key: "dedicated", Operator: "Equal", Value: "bench", Effect: "NoSchedule"}}}
}

// Nodes rank by free CPU (allocatable minus the requests of the pods bound to them, finished pods excluded); nodes
// that cannot take the pod follow with the reason.
func TestRankNodes(t *testing.T) {
	var nodes struct{ Items []k8sNode }
	var pods struct{ Items []k8sPod }
	if err := jsonUnmarshal(testNodes, &nodes); err != nil {
		t.Fatal(err)
	}
	if err := jsonUnmarshal(testPods, &pods); err != nil {
		t.Fatal(err)
	}
	rooms, err := rankNodes(nodes.Items, pods.Items, testRoomValues())
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	skip := map[string]string{}
	for _, r := range rooms {
		order = append(order, r.Name)
		skip[r.Name] = r.Skip
	}
	if want := []string{"idle", "tainted-ok", "half", "busy"}; !slices.Equal(order[:4], want) {
		t.Errorf("order %v, want %v first", order, want)
	}
	for name, why := range map[string]string{
		"down": "not Ready", "cordoned": "cordoned", "other-pool": "node selector", "gpu": "taint gpu:NoExecute",
		"full": "1.50 CPUs free, the pod needs 2", "no-memory": "6.0 GiB free, the pod needs 8Gi",
	} {
		if !strings.Contains(skip[name], why) {
			t.Errorf("%s skipped for %q, want %q", name, skip[name], why)
		}
	}
	for _, r := range rooms {
		if r.Name == "half" && (r.FreeCPU != 27.5 || r.Pods != 1 || r.FreeMem != 370<<30) {
			t.Errorf("half: %+v", r)
		}
	}
}

// routeKubectl answers kubectl calls by their leading arguments.
type routeKubectl struct {
	routes map[string]string
	calls  []string
}

func (k *routeKubectl) run(_ context.Context, _ []byte, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	k.calls = append(k.calls, key)
	best := ""
	for prefix := range k.routes {
		if strings.HasPrefix(key, prefix) && len(prefix) > len(best) {
			best = prefix
		}
	}
	switch out := k.routes[best]; {
	case best == "":
		return nil, errors.New("unexpected kubectl call: " + key)
	case out == "forbidden":
		return nil, errors.New(`Error from server (Forbidden): User "u" cannot list the resource at the cluster scope`)
	case out == "unreachable":
		return nil, errors.New(`Unable to connect to the server: dial tcp: i/o timeout`)
	default:
		return []byte(out), nil
	}
}

func TestPickNodes(t *testing.T) {
	k := &routeKubectl{routes: map[string]string{"get nodes -o json": testNodes, "get pods --all-namespaces": testPods}}
	var progress bytes.Buffer
	picked, ranking, err := pickNodes(context.Background(), k, testRoomValues(), 3, nil, &progress)
	if err != nil || !slices.Equal(picked, []string{"idle", "tainted-ok", "half"}) || ranking.Basis != basisCluster || len(ranking.Nodes) != 10 {
		t.Fatalf("picked %v, %s, %v", picked, ranking.Basis, err)
	}
	if !slices.Contains(k.calls, "get pods --all-namespaces -o json --field-selector status.phase!=Succeeded,status.phase!=Failed") {
		t.Errorf("calls %v", k.calls)
	}
	if !strings.Contains(progress.String(), "* idle") || !strings.Contains(progress.String(), "cordoned") {
		t.Errorf("progress:\n%s", progress.String())
	}
	if _, _, err := pickNodes(context.Background(), k, testRoomValues(), 5, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "4 nodes can take the pod, 5 wanted") {
		t.Errorf("too few nodes: %v", err)
	}
	// Candidates restrict the ranking to themselves.
	picked, ranking, err = pickNodes(context.Background(), k, testRoomValues(), 1, []string{"busy", "half", "cordoned"}, &bytes.Buffer{})
	if err != nil || !slices.Equal(picked, []string{"half"}) || len(ranking.Nodes) != 3 {
		t.Errorf("candidates: %v %+v %v", picked, ranking, err)
	}
	if _, _, err := pickNodes(context.Background(), k, testRoomValues(), 1, []string{"busy", "missing"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "-nodes names missing, which is not a node of the cluster") {
		t.Errorf("unknown candidate: %v", err)
	}
}

// The session namespace's own pods, the only pods a namespace-scoped account can list.
const ownPods = `{"items":[
 {"spec":{"nodeName":"idle","containers":[{"resources":{"requests":{"cpu":"30"}}}]},"status":{"phase":"Running"}},
 {"spec":{"nodeName":"c1","containers":[{"resources":{"requests":{"cpu":"2"}}}]},"status":{"phase":"Running"}},
 {"spec":{"nodeName":"c2","containers":[{"resources":{"requests":{"cpu":"4"}}}]},"status":{"phase":"Succeeded"}}
]}`

// Without a cluster-wide pod list, the ranking falls back to the namespace's pods and says so.
func TestPickNodesWithoutClusterWidePods(t *testing.T) {
	v := testRoomValues()
	v.Namespace = "bench-ns"
	k := &routeKubectl{routes: map[string]string{"get nodes -o json": testNodes, "get pods --all-namespaces": "forbidden", "get pods -o json --field-selector status.phase!=Succeeded,status.phase!=Failed": ownPods}}
	var progress bytes.Buffer
	picked, ranking, err := pickNodes(context.Background(), k, v, 3, nil, &progress)
	// busy's 40 CPUs belong to another namespace and are invisible: it ranks with the idle nodes; idle's own 30 CPUs count.
	if err != nil || ranking.Basis != basisNamespace || !slices.Equal(picked, []string{"busy", "full", "tainted-ok"}) {
		t.Fatalf("picked %v, %s, %v", picked, ranking.Basis, err)
	}
	if !strings.Contains(progress.String(), "the ranking counts only the requests of namespace bench-ns, so free CPU is an upper bound") {
		t.Errorf("progress:\n%s", progress.String())
	}
	k.routes["get pods -o json --field-selector status.phase!=Succeeded,status.phase!=Failed"] = "forbidden"
	if _, _, err := pickNodes(context.Background(), k, v, 1, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "picking a node needs list on pods, at least in the namespace") {
		t.Errorf("no pod list at all: %v", err)
	}
	// Only a Forbidden answer narrows the ranking: a failed cluster-wide list ends the pick instead of ranking a busy
	// node first under a warning that blames permissions.
	k.routes["get pods -o json --field-selector status.phase!=Succeeded,status.phase!=Failed"] = ownPods
	k.routes["get pods --all-namespaces"] = "unreachable"
	k.calls = nil
	if _, ranking, err := pickNodes(context.Background(), k, v, 1, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "Unable to connect to the server") || ranking.Basis == basisNamespace || slices.ContainsFunc(k.calls, func(c string) bool { return strings.HasPrefix(c, "get pods -o json") }) {
		t.Errorf("unreachable cluster-wide pod list: %s, %v, calls %v", ranking.Basis, err, k.calls)
	}
}

// Without a node list, the ranking needs -nodes candidates and orders them by the namespace's own requests, then as
// given.
func TestPickNodesWithoutANodeList(t *testing.T) {
	v := testRoomValues()
	v.Namespace = "bench-ns"
	k := &routeKubectl{routes: map[string]string{"get nodes": "forbidden", "get pods --all-namespaces": "forbidden", "get pods -o json": ownPods}}
	var progress bytes.Buffer
	picked, ranking, err := pickNodes(context.Background(), k, v, 2, []string{"c1", "c2", "c3"}, &progress)
	if err != nil || ranking.Basis != basisCandidates || !slices.Equal(picked, []string{"c2", "c3"}) || ranking.Nodes[2].Name != "c1" || ranking.Nodes[2].Pods != 1 {
		t.Fatalf("picked %v, %+v, %v", picked, ranking, err)
	}
	if !strings.Contains(progress.String(), "nodes are not listable; ranking the -nodes candidates") || !strings.Contains(progress.String(), "c1                                       allocatable unknown,   2.00 CPUs requested") {
		t.Errorf("progress:\n%s", progress.String())
	}
	if _, _, err := pickNodes(context.Background(), k, v, 1, nil, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "name the candidates with -nodes a,b,c") {
		t.Errorf("nothing to rank: %v", err)
	}
	// A node list that fails for another reason than permission ends the pick, candidates or not.
	k.routes["get nodes"] = "unreachable"
	if _, ranking, err := pickNodes(context.Background(), k, v, 1, []string{"c1", "c2"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "Unable to connect to the server") || ranking.Basis == basisCandidates {
		t.Errorf("unreachable node list: %s, %v", ranking.Basis, err)
	}
}

func TestNodeFlagsConflict(t *testing.T) {
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	for _, args := range [][]string{
		{"-node", "a", "-spread", "2"},
		{"-node", "a", "-pick-node"},
		{"-nodes", "a,b", "-spread", "3"},
		{"-nodes", "a,b", "-node", "a", "-spread", "1"},
		{"-nodes", "a,b", "-node", "c"},
		{"-nodes", "a,B_"},
		{"-nodes", "a,b,a"},
		{"-spread", "-1"},
	} {
		var stderr bytes.Buffer
		code := run(context.Background(), append([]string{"compare", "-values", os.DevNull, "-dry-run"}, args...), &bytes.Buffer{}, &stderr, func(k string) string { return env[k] })
		if code != 1 {
			t.Errorf("%v: exit %d: %s", args, code, stderr.String())
		}
	}
}

// A dry run of a spread session prints one manifest per node and contacts no cluster; when the nodes are to be
// picked it says so.
func TestDryRunSpread(t *testing.T) {
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	missing := filepath.Join(t.TempDir(), "no-kubectl")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"compare", "-values", os.DevNull, "-dry-run", "-kubectl", missing, "-nodes", "node-a,node-b"}, &stdout, &stderr, func(k string) string { return env[k] })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	docs := strings.Split(stdout.String(), "---\n")
	if len(docs) != 2 || !strings.Contains(docs[0], "node-a") || !strings.Contains(docs[1], "node-b") || !strings.Contains(docs[1], "-n2") {
		t.Errorf("manifests:\n%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"compare", "-values", os.DevNull, "-dry-run", "-kubectl", missing, "-spread", "3"}, &stdout, &stderr, func(k string) string { return env[k] })
	if code != 0 || !strings.Contains(stderr.String(), "the run picks 3 node(s) by free CPU") || strings.Contains(stdout.String(), "---") {
		t.Errorf("exit %d: %s\n%s", code, stderr.String(), stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"compare", "-values", os.DevNull, "-dry-run", "-kubectl", missing, "-nodes", "node-a,node-b,node-c", "-spread", "2"}, &stdout, &stderr, func(k string) string { return env[k] })
	docs = strings.Split(stdout.String(), "---\n")
	if code != 0 || !strings.Contains(stderr.String(), "the run picks 2 of the -nodes candidates") || len(docs) != 2 || strings.Contains(stdout.String(), "node-c") {
		t.Errorf("exit %d: %s\n%s", code, stderr.String(), stdout.String())
	}
}

func jsonUnmarshal(text string, v any) error { return json.Unmarshal([]byte(text), v) }

// A spread calibration through a kubectl stand-in: the runner picks the two nodes with the most free CPU, runs one
// Job pinned to each at once, collects both, and reports the median over them.
func TestSpreadCalibrationThroughKubectl(t *testing.T) {
	spreadCalibrationThroughKubectl(t, `"get nodes -o json") cat nodes.json ;;
"get pods --all-namespaces "*) cat pods.json ;;`, "-spread", "2")
}

// With namespace-scoped RBAC (no node list, no pods in other namespaces) the runner ranks the -nodes candidates by
// the CPU its own namespace requests on them, then in the order given.
func TestSpreadCalibrationThroughKubectlWithNamespaceRBAC(t *testing.T) {
	ownPods := `{"items":[{"spec":{"nodeName":"busy","containers":[{"resources":{"requests":{"cpu":"1"}}}]},"status":{"phase":"Running"}}]}`
	spreadCalibrationThroughKubectl(t, `"get pods -o json --field-selector "*) echo '`+ownPods+`' ;;`, "-nodes", "busy,other-pool,idle", "-spread", "2")
}

func spreadCalibrationThroughKubectl(t *testing.T, listing string, nodeArgs ...string) {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{
		"nodes.json": testNodes, "pods.json": testPods,
		"log1": synth{mode: modeCalibrate, node: "other-pool", cpuSet: "shared", runs: 12}.text(t),
		"log2": synth{mode: modeCalibrate, node: "idle", cpuSet: "shared", runs: 12}.text(t),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pod := func(name, node string) string {
		return `echo '{"items":[{"metadata":{"name":"` + name + `"},"spec":{"nodeName":"` + node + `"},"status":{"phase":"Running"}}]}'`
	}
	script := `#!/bin/sh
while [ $# -gt 0 ]; do case $1 in --kubeconfig|--context|--namespace) shift 2 ;; *) break ;; esac; done
cd ` + dir + `
echo "$*" >> calls
case "$*" in
` + listing + `
"apply -f -") cat > applied-$$.yaml ;;
"get job "*) echo '{"status":{"conditions":[{"type":"Complete","status":"True"}]}}' ;;
"get pods -l job-name="*-n1" -o json") ` + pod("p1", "other-pool") + ` ;;
"get pods -l job-name="*-n2" -o json") ` + pod("p2", "idle") + ` ;;
"logs pod/p1 -c bench") cat log1 ;;
"logs pod/p2 -c bench") cat log2 ;;
"delete job "*) ;;
*) echo "Error from server (Forbidden): $* is forbidden" >&2; exit 1 ;;
esac
`
	kubectlPath := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	var stdout, stderr bytes.Buffer
	args := append([]string{"calibrate", "-values", os.DevNull, "-kubectl", kubectlPath, "-poll", "1ms", "-out", out, "-turns", "50", "-runs", "12"}, nodeArgs...)
	code := run(context.Background(), args, &stdout, &stderr, func(k string) string { return env[k] })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	runs, _ := filepath.Glob(filepath.Join(out, "k8sbench-cal-*"))
	if len(runs) != 1 {
		t.Fatalf("run directories %v", runs)
	}
	for _, f := range []string{"spread.json", "nodes.json", "calibration.json", filepath.Base(runs[0]) + "-n1/pod.log", filepath.Base(runs[0]) + "-n2/cluster.json"} {
		if _, err := os.Stat(filepath.Join(runs[0], f)); err != nil {
			t.Error(err)
		}
	}
	cal, _ := os.ReadFile(filepath.Join(runs[0], "calibration.json"))
	if !strings.Contains(string(cal), `"nodes": [
    "other-pool",
    "idle"
  ]`) || !strings.Contains(stdout.String(), "median over 2 nodes: other-pool, idle") {
		t.Errorf("calibration.json:\n%s\nstdout:\n%s", cal, stdout.String())
	}
	applied, _ := filepath.Glob(filepath.Join(dir, "applied-*.yaml"))
	var manifests string
	for _, a := range applied {
		data, _ := os.ReadFile(a)
		manifests += string(data)
	}
	if len(applied) != 2 || !strings.Contains(manifests, `"other-pool"`) || !strings.Contains(manifests, `"idle"`) {
		t.Errorf("applied %d manifests:\n%s", len(applied), manifests)
	}
}

// A comparison against a calibration that cannot vouch for it is refused before a Job occupies a node: the session
// must be pinned to calibrated nodes, and the calibration must be judged at every size.
func TestCompareChecksTheCalibrationBeforeSubmitting(t *testing.T) {
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	dir := t.TempDir()
	cal := filepath.Join(dir, "calibration.json")
	writeCalibration(t, cal, 12, "node-a", "node-b")
	smoke := filepath.Join(dir, "smoke.json")
	writeCalibration(t, smoke, 6, "node-a")
	missing := filepath.Join(dir, "no-kubectl")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"-calibration", cal}, "pin the session to calibrated nodes"},
		{[]string{"-calibration", cal, "-node", "node-c"}, "not node-c"},
		{[]string{"-calibration", cal, "-nodes", "node-a,node-c"}, "not node-c"},
		{[]string{"-calibration", cal, "-node", "node-a", "-turns", "50,250"}, "no calibration at 250 turns"},
		{[]string{"-calibration", smoke, "-node", "node-a"}, "has 6 runs at 50 turns"},
		{[]string{"-calibration", filepath.Join(dir, "absent.json"), "-node", "node-a"}, "absent.json"},
	} {
		var stderr bytes.Buffer
		args := append([]string{"compare", "-values", os.DevNull, "-kubectl", missing, "-out", t.TempDir(), "-turns", "50"}, c.args...)
		code := run(context.Background(), args, &bytes.Buffer{}, &stderr, func(k string) string { return env[k] })
		if code != 1 || !strings.Contains(stderr.String(), c.want) || strings.Contains(stderr.String(), "no-kubectl") {
			t.Errorf("%v: exit %d, want a refusal naming %q before kubectl runs:\n%s", c.args, code, c.want, stderr.String())
		}
	}
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"compare", "-values", os.DevNull, "-kubectl", missing, "-dry-run", "-turns", "50", "-calibration", cal, "-nodes", "node-a,node-b"}, &bytes.Buffer{}, &stderr, func(k string) string { return env[k] })
	if code != 0 {
		t.Errorf("a calibration of the session's nodes: exit %d: %s", code, stderr.String())
	}
}

// When one node's Job of a spread session fails, the session cannot report a median, so the runner stops and
// deletes the other nodes' Jobs instead of waiting hours for them.
func TestSpreadStopsWhenOneJobFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "log1"), []byte(`{"t":"error","message":"seed failed on this node"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pod := func(name, node string) string {
		return `echo '{"items":[{"metadata":{"name":"` + name + `"},"spec":{"nodeName":"` + node + `"},"status":{"phase":"Running"}}]}'`
	}
	script := `#!/bin/sh
while [ $# -gt 0 ]; do case $1 in --kubeconfig|--context|--namespace) shift 2 ;; *) break ;; esac; done
cd ` + dir + `
echo "$*" >> calls
case "$*" in
"apply -f -") cat > /dev/null ;;
"get job "*-n1" -o json") echo '{"status":{"conditions":[{"type":"Failed","status":"True","reason":"BackoffLimitExceeded"}]}}' ;;
"get job "*-n2" -o json") echo '{"status":{}}' ;;
"get pods -l job-name="*-n1" -o json") ` + pod("p1", "node-a") + ` ;;
"get pods -l job-name="*-n2" -o json") ` + pod("p2", "node-b") + ` ;;
"logs pod/p1 -c bench") cat log1 ;;
"delete job "*) ;;
*) echo "forbidden: $*" >&2; exit 1 ;;
esac
`
	kubectlPath := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_IMAGE": testImage}
	// The bound only ends a runner that waits for the healthy Job: without the stop it would wait for the deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	code := run(ctx, []string{"compare", "-values", os.DevNull, "-kubectl", kubectlPath, "-poll", "1ms", "-out", t.TempDir(), "-turns", "50", "-nodes", "node-a,node-b"}, &bytes.Buffer{}, &stderr, func(k string) string { return env[k] })
	if ctx.Err() != nil {
		t.Fatal("the runner waited for the other node's Job after one failed")
	}
	if code != 1 || !strings.Contains(stderr.String(), "seed failed on this node") || !strings.Contains(stderr.String(), "stopped because another node's Job failed") {
		t.Errorf("exit %d:\n%s", code, stderr.String())
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !regexp.MustCompile(`(?m)^delete job k8sbench-cmp-\S+-n2 `).Match(calls) {
		t.Errorf("the other node's Job was not deleted:\n%s", calls)
	}
}
