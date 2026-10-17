package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

const repoRoot = "../../../.."

func gpuTestValues() (values, gpuValues) {
	v := defaultValues()
	v.Namespace = "bench"
	return v, gpuValues{ModelPVC: "weights", TrialPVC: "trials"}.withDefaults()
}

var stagedSum = strings.Repeat("ab", 32)

func TestCommittedGPULocksValidate(t *testing.T) {
	ml, err := readModelsLock(filepath.Join(repoRoot, modelsLockFile))
	if err != nil {
		t.Fatal(err)
	}
	engines := map[string]bool{}
	for _, m := range ml.Models {
		engines[m.Engine] = true
	}
	if !engines[engineVLLM] || !engines[engineLlamaCPP] {
		t.Fatalf("models.lock pins a model for each engine; got %v", engines)
	}
	if err := ml.checkPinAges(time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if err := ml.checkPinAges(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("a pin younger than seven days was accepted")
	}
	tl, err := readTasksLock(filepath.Join(repoRoot, tasksLockFile))
	if err != nil {
		t.Fatal(err)
	}
	if len(tl.Tasks) == 0 {
		t.Fatal("tasks.lock pins no tasks")
	}
}

func TestModelLockRefusesLoosePins(t *testing.T) {
	engines := map[string]engineLock{engineVLLM: {}, engineLlamaCPP: {}}
	good := modelLock{Engine: engineLlamaCPP, Repo: "org/name", Revision: strings.Repeat("a", 40), Published: "2026-01-01",
		ContextWindow: 4096, MaxTokens: 512, Files: []modelFile{{Path: "m.gguf", Size: 1, SHA256: stagedSum}}}
	if err := good.validate("m", engines); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*modelLock){
		"short revision": func(m *modelLock) { m.Revision = "main" },
		"no sha":         func(m *modelLock) { m.Files[0].SHA256 = "" },
		"escaping path":  func(m *modelLock) { m.Files[0].Path = "../m.gguf" },
		"two ggufs":      func(m *modelLock) { m.Files = append(m.Files, modelFile{Path: "n.gguf", Size: 1, SHA256: stagedSum}) },
		"no gguf":        func(m *modelLock) { m.Files[0].Path = "m.bin" },
		"unknown engine": func(m *modelLock) { m.Engine = "other" },
		"window":         func(m *modelLock) { m.MaxTokens = m.ContextWindow },
	} {
		m := good
		m.Files = slices.Clone(good.Files)
		mutate(&m)
		if err := m.validate("m", engines); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	l := modelsLock{Engines: map[string]engineLock{engineVLLM: {Image: "vllm/vllm-openai:v1", Published: "2026-01-01"}}}
	if err := l.validate(); err == nil {
		t.Error("an engine image without a digest was accepted")
	}
}

// decoded round-trips the rendered YAML, as kubectl reads it.
func decoded(t *testing.T, objects []k8sObject) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if err := writeObjects(&buf, objects); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	dec := yaml.NewDecoder(&buf)
	for {
		var o map[string]any
		if err := dec.Decode(&o); err != nil {
			break
		}
		out = append(out, o)
	}
	if len(out) != len(objects) {
		t.Fatalf("decoded %d objects from %d", len(out), len(objects))
	}
	return out
}

func dig(o any, keys ...any) any {
	for _, k := range keys {
		switch k := k.(type) {
		case string:
			o = o.(map[string]any)[k]
		case int:
			o = o.([]any)[k]
		}
	}
	return o
}

func TestRenderModelServesTheLockedWeights(t *testing.T) {
	ml, err := readModelsLock(filepath.Join(repoRoot, modelsLockFile))
	if err != nil {
		t.Fatal(err)
	}
	v, g := gpuTestValues()
	g.Count = 2
	g.Resource = "amd.com/gpu"
	g.NodeSelector = map[string]string{"pool": "gpu"}
	for id, m := range ml.Models {
		objects, err := renderModel(v, g, ml, id, stagedSum)
		if err != nil {
			t.Fatal(err)
		}
		docs := decoded(t, objects)
		kinds := []string{}
		for _, d := range docs {
			kinds = append(kinds, d["kind"].(string))
		}
		if !slices.Equal(kinds, []string{"Job", "Deployment", "Service", "NetworkPolicy"}) {
			t.Fatalf("%s: kinds %v", id, kinds)
		}
		fetch := dig(docs[0], "spec", "template", "spec", "containers", 0).(map[string]any)
		if cmd := fmt.Sprint(fetch["command"]); !strings.Contains(cmd, stageMount+"/bundles/"+stagedSum+"/bin/k8sbench model-fetch") {
			t.Errorf("%s: fetch command %s", id, cmd)
		}
		server := dig(docs[1], "spec", "template", "spec", "containers", 0).(map[string]any)
		if server["image"] != ml.Engines[m.Engine].Image {
			t.Errorf("%s: server image %v", id, server["image"])
		}
		if got := dig(server, "resources", "limits", "amd.com/gpu"); got != "2" {
			t.Errorf("%s: gpu limit %v", id, got)
		}
		if got := dig(docs[1], "spec", "template", "spec", "nodeSelector", "pool"); got != "gpu" {
			t.Errorf("%s: node selector %v", id, got)
		}
		cmd := fmt.Sprint(server["command"])
		pin := dig(fetch, "env", 0, "value").(string)
		for _, want := range append([]string{fmt.Sprint(m.ContextWindow), modelsMount + "/" + id, verifiedMarker, pinDigest(pin)}, m.Args...) {
			if !strings.Contains(cmd, want) {
				t.Errorf("%s: server command lacks %q: %s", id, want, cmd)
			}
		}
		if m.Engine == engineVLLM && !strings.Contains(cmd, "--tensor-parallel-size 2") {
			t.Errorf("%s: vLLM does not use both GPUs: %s", id, cmd)
		}
		if got := dig(server, "volumeMounts", 0, "readOnly"); got != true {
			t.Errorf("%s: the server mounts the weights writable", id)
		}
		from := dig(docs[3], "spec", "ingress", 0, "from", 0, "podSelector", "matchLabels").(map[string]any)
		if from["k8sbench/role"] != "trial" || from["k8sbench/model"] != id {
			t.Errorf("%s: policy admits %v", id, from)
		}
		for _, i := range []int{0, 1} {
			spec := dig(docs[i], "spec", "template", "spec").(map[string]any)
			if spec["automountServiceAccountToken"] != false || spec["enableServiceLinks"] != false {
				t.Errorf("%s: %s pod gets an API token or service links", id, docs[i]["kind"])
			}
		}
		name := dig(docs[2], "metadata", "name").(string)
		if want := "http://" + name + ".bench.svc:8000/v1"; modelURL(id, "bench") != want {
			t.Errorf("%s: url %s, want %s", id, modelURL(id, "bench"), want)
		}
	}
	if _, err := renderModel(v, g, ml, "absent", stagedSum); err == nil {
		t.Error("an unlocked model rendered")
	}
}

func TestRenderTrialsHidesTheTestsFromTheAgent(t *testing.T) {
	ml, err := readModelsLock(filepath.Join(repoRoot, modelsLockFile))
	if err != nil {
		t.Fatal(err)
	}
	tl, err := readTasksLock(filepath.Join(repoRoot, tasksLockFile))
	if err != nil {
		t.Fatal(err)
	}
	v, g := gpuTestValues()
	model := slices.Sorted(func(yield func(string) bool) {
		for id := range ml.Models {
			if !yield(id) {
				return
			}
		}
	})[0]
	tasks := []string{tl.Tasks[0].Name, tl.Tasks[len(tl.Tasks)-1].Name}
	objects, err := renderTrials(v, g, ml, tl, model, stagedSum, []string{armPig, armPi}, tasks, 3)
	if err != nil {
		t.Fatal(err)
	}
	docs := decoded(t, objects)
	if len(docs) != 2*2*3 {
		t.Fatalf("%d jobs, want 12", len(docs))
	}
	names := map[string]bool{}
	for _, d := range docs {
		names[dig(d, "metadata", "name").(string)] = true
		spec := dig(d, "spec", "template", "spec").(map[string]any)
		task, _ := tl.find(dig(d, "metadata", "labels", "k8sbench/task").(string))
		bench := dig(spec, "containers", 0).(map[string]any)
		if bench["image"] != task.Image {
			t.Errorf("trial image %v, want the task's %s", bench["image"], task.Image)
		}
		readOnly := func(container map[string]any) map[string]any {
			out := map[string]any{}
			for _, m := range container["volumeMounts"].([]any) {
				out[m.(map[string]any)["name"].(string)] = m.(map[string]any)["readOnly"]
			}
			return out
		}
		if mounts := readOnly(bench); mounts["stage"] != nil || mounts["answer"] != true || mounts["request"] != false {
			t.Errorf("the agent's container mounts %v: it must not see the claim with the tests, nor write the gate's answer", mounts)
		}
		if mounts := readOnly(dig(spec, "containers", 1).(map[string]any)); mounts["stage"] != true || mounts["request"] != true || mounts["answer"] != false {
			t.Errorf("the verifier gate mounts %v", mounts)
		}
		if spec["automountServiceAccountToken"] != false || spec["enableServiceLinks"] != false {
			t.Error("the trial pod gets an API token or service links")
		}
		var tp trialPlan
		envs := bench["env"].([]any)
		if err := json.Unmarshal([]byte(dig(envs, 0, "value").(string)), &tp); err != nil {
			t.Fatal(err)
		}
		if tp.Task.Files != task.Files || tp.BaseURL != modelURL(model, "bench") || tp.ContextWindow != ml.Models[model].ContextWindow {
			t.Errorf("trial plan %+v", tp)
		}
		if dig(spec, "containers", 1, "name") != "verifier-gate" || dig(spec, "volumes", 0, "persistentVolumeClaim", "claimName") != "trials" {
			t.Error("the verifier gate does not read the trial claim")
		}
		if got := dig(d, "spec", "template", "metadata", "labels", "k8sbench/role"); got != "trial" {
			t.Errorf("role label %v: the server's policy would refuse the trial", got)
		}
	}
	if len(names) != len(docs) {
		t.Errorf("%d unique Job names for %d jobs", len(names), len(docs))
	}
	if _, err := renderTrials(v, g, ml, tl, model, stagedSum, []string{"claude"}, nil, 1); err == nil {
		t.Error("an unknown arm rendered")
	}
	if _, err := renderTrials(v, g, ml, tl, model, "short", []string{armPig}, nil, 1); err == nil {
		t.Error("a bad -staged rendered")
	}
}

// writeTask writes a task directory with an instruction and a test script, returning its files digest.
func writeTask(t *testing.T, dir, testScript string) string {
	t.Helper()
	for path, body := range map[string]string{"instruction.md": "solve it\n", "tests/test.sh": testScript, "tests/data/x.txt": "x\n", "solution/solve.sh": "secret\n"} {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := taskFilesDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestAddTasksCopiesOnlyLockedFiles(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	sum := writeTask(t, filepath.Join(src, "one"), "exit 0\n")
	l := &tasksLock{Dataset: "d", Repo: "https://example.invalid/d", Commit: strings.Repeat("c", 40), Published: "2026-01-01",
		Tasks: []taskLock{{Name: "one", Image: "x@sha256:" + stagedSum, Files: sum, AgentTimeoutSec: 1, VerifierTimeoutSec: 1, CPUs: 1, MemoryMB: 1, StorageMB: 1}}}
	if err := addTasks(src, out, l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "tasks", "one", "solution")); err == nil {
		t.Error("the solution was copied into the bundle")
	}
	if got, err := taskFilesDigest(filepath.Join(out, "tasks", "one")); err != nil || got != sum {
		t.Errorf("copied digest %s %v", got, err)
	}
	arts, err := taskArtifacts(out)
	if err != nil || !slices.Contains(arts, "tasks/one/tests/data/x.txt") || !slices.Contains(arts, "tasks/tasks.lock") {
		t.Errorf("artifacts %v %v", arts, err)
	}
	if err := os.WriteFile(filepath.Join(src, "one", "tests", "test.sh"), []byte("exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := addTasks(src, t.TempDir(), l); err == nil {
		t.Error("a task whose tests changed was copied")
	}
}

func TestLockTasksPinsImagesAndSkipsUnrunnable(t *testing.T) {
	src := t.TempDir()
	task := func(name, env string) {
		writeTask(t, filepath.Join(src, name), "exit 0\n")
		toml := "[agent]\ntimeout_sec = 60.0\n[verifier]\ntimeout_sec = 30.0\n[environment]\ncpus = 2\nmemory_mb = 4096\nstorage_mb = 10240\nallow_internet = true\n" + env
		if err := os.WriteFile(filepath.Join(src, name, "task.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	task("ok", "docker_image = \"someone/ok:1\"\n")
	task("built", "")
	task("gpu", "docker_image = \"someone/gpu:1\"\ngpus = 1\n")
	var asked []string
	digest := func(_ context.Context, ref string) (string, error) {
		asked = append(asked, ref)
		return "sha256:" + stagedSum, nil
	}
	var progress bytes.Buffer
	l, err := lockTasks(context.Background(), src, tasksLock{Dataset: "d", Repo: "https://example.invalid/d", Commit: strings.Repeat("c", 40), Published: "2026-01-01"}, digest, &progress)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Tasks) != 1 || l.Tasks[0].Image != "docker.io/someone/ok:1@sha256:"+stagedSum || l.Tasks[0].CPUs != 2 || l.Tasks[0].AgentTimeoutSec != 60 {
		t.Fatalf("tasks %+v", l.Tasks)
	}
	if !slices.Equal(asked, []string{"someone/ok:1"}) || !strings.Contains(progress.String(), "skip built") || !strings.Contains(progress.String(), "skip gpu") {
		t.Errorf("asked %v, progress %s", asked, progress.String())
	}
}

func TestSplitImageRef(t *testing.T) {
	for ref, want := range map[string][3]string{
		"someone/img:20251031":           {"registry-1.docker.io", "someone/img", "20251031"},
		"ubuntu":                         {"registry-1.docker.io", "library/ubuntu", "latest"},
		"docker.io/someone/img:1":        {"registry-1.docker.io", "someone/img", "1"},
		"ghcr.io/org/repo/img:server-b1": {"ghcr.io", "org/repo/img", "server-b1"},
		"localhost:5000/img":             {"localhost:5000", "img", "latest"},
	} {
		host, repo, tag, err := splitImageRef(ref)
		if err != nil || [3]string{host, repo, tag} != want {
			t.Errorf("%s: %s %s %s %v, want %v", ref, host, repo, tag, err, want)
		}
	}
	if _, _, _, err := splitImageRef("img@sha256:" + stagedSum); err == nil {
		t.Error("a pinned reference was split")
	}
}

func TestUsageWriterSumsAssistantMessagesAcrossWrites(t *testing.T) {
	var u usageWriter
	stream := `{"type":"message_end","message":{"role":"user"}}
{"type":"message_end","message":{"role":"assistant","usage":{"input":10,"output":5,"cacheRead":2,"cacheWrite":1,"totalTokens":18}}}
not json
{"type":"message_update","message":{"role":"assistant","usage":{"input":99}}}
{"type":"message_end","message":{"role":"assistant","usage":{"input":7,"output":3,"totalTokens":10}}}`
	for chunk := range slices.Chunk([]byte(stream), 7) {
		if _, err := u.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	u.flush()
	want := trialUsage{Input: 17, Output: 8, CacheRead: 2, CacheWrite: 1, TotalTokens: 28}
	if u.total != want || u.messages != 2 {
		t.Fatalf("usage %+v from %d messages, want %+v from 2", u.total, u.messages, want)
	}
}

// trialFixture lays out a trial pod's directories with a fake agent and a task whose test passes when the agent
// wrote the expected answer. gate stands in for the verifier gate container.
func trialFixture(t *testing.T, agent, testScript string) (trialPlan, trialDirs, func()) {
	t.Helper()
	base := t.TempDir()
	d := trialDirs{root: filepath.Join(base, "root"), request: filepath.Join(base, "request"), answer: filepath.Join(base, "answer-dir"), logs: filepath.Join(base, "logs"), tests: filepath.Join(base, "tests")}
	staged := filepath.Join(base, "staged", "one")
	sum := writeTask(t, staged, testScript)
	for _, dir := range []string{filepath.Join(d.root, "bin"), filepath.Join(d.root, "task"), d.request, d.answer} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyFile(filepath.Join(staged, "instruction.md"), filepath.Join(d.root, "task", "instruction.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.root, "bin", "pig"), []byte(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_WORK", filepath.Join(base, "answer"))
	t.Setenv("FAKE_LOGS", d.logs)
	t.Setenv("FAKE_REQUEST", d.request)
	t.Setenv("FAKE_ANSWER", d.answer)
	tp := trialPlan{Task: taskLock{Name: "one", Image: "img@sha256:" + stagedSum, Files: sum, AgentTimeoutSec: 5, VerifierTimeoutSec: 5, CPUs: 1, MemoryMB: 1, StorageMB: 1},
		Arm: armPig, Attempt: 1, Model: "m", BaseURL: "http://m.bench.svc:8000/v1", ContextWindow: 4096, MaxTokens: 512, TestsTimeoutSeconds: 5}
	// The verifier gate container's own script. Calling gate starts it and returns: it polls for the trial's request every
	// second, so a trial that never writes one (a refused lock, a failed prepare) would leave it polling, and the sleep
	// it runs, after the test. The cleanup hands it a request that asks for no tests, which ends the script, and waits
	// for it.
	gate := func() {
		cmd := exec.Command("sh", "-c", gateScript)
		cmd.Env = append(os.Environ(), "K8SBENCH_GATE_REQUEST="+d.request, "K8SBENCH_GATE_ANSWER="+d.answer, "K8SBENCH_GATE_TESTS="+filepath.Join(staged, "tests"))
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Start(); err != nil {
			t.Fatalf("gate: %v", err)
		}
		finished := make(chan error, 1)
		go func() { finished <- cmd.Wait() }()
		t.Cleanup(func() {
			if data, err := os.ReadFile(filepath.Join(d.request, "agent-done")); err != nil || len(data) == 0 {
				if err := writeAtomic(filepath.Join(d.request, "agent-done"), []byte("stop")); err != nil {
					t.Errorf("end the gate: %v", err)
				}
			}
			if err := <-finished; err != nil {
				t.Errorf("gate: %v: %s", err, out.String())
			}
		})
	}
	return tp, d, gate
}

const fakeAgent = `#!/bin/sh
[ "$1" = --version ] && exit 0
# the tests must not be visible while the agent runs
[ -e "$FAKE_LOGS/../tests" ] && { echo "tests visible" >&2; exit 9; }
grep -q '"baseUrl": "http://m.bench.svc:8000/v1"' "$PIG_CODING_AGENT_DIR/models.json" || exit 8
echo '{"type":"message_end","message":{"role":"assistant","usage":{"input":3,"output":4,"totalTokens":7}}}'
echo answer > "$FAKE_WORK"
`

const passingTest = `#!/bin/bash
mkdir -p "$FAKE_LOGS/verifier"
if grep -q answer "$FAKE_WORK"; then echo 1 > "$FAKE_LOGS/verifier/reward.txt"; else echo 0 > "$FAKE_LOGS/verifier/reward.txt"; fi
`

func TestRunTrialScoresTheAgentAfterItExits(t *testing.T) {
	tp, d, gate := trialFixture(t, fakeAgent, passingTest)
	gate()
	res := runTrial(context.Background(), tp, d)
	if res.Error != "" || res.AgentError != "" || res.Reward == nil || *res.Reward != 1 {
		t.Fatalf("result %+v", res)
	}
	if res.Usage.TotalTokens != 7 || res.AssistantMessages != 1 || res.AgentExit != 0 || res.VerifierExit != 0 {
		t.Errorf("result %+v", res)
	}
	if data, _ := os.ReadFile(filepath.Join(d.answer, "answer")); !strings.HasPrefix(string(data), "run ") || len(strings.TrimSpace(string(data))) != len("run ")+32 {
		t.Errorf("gate answered %q", data)
	}
}

func TestRunTrialVerifiesAFailedOrTimedOutAgent(t *testing.T) {
	tp, d, gate := trialFixture(t, "#!/bin/sh\n[ \"$1\" = --version ] && exit 0\necho broke >&2\nexit 3\n", passingTest)
	gate()
	res := runTrial(context.Background(), tp, d)
	if res.AgentExit != 3 || !strings.Contains(res.AgentError, "broke") || res.Reward == nil || *res.Reward != 0 || res.Error != "" {
		t.Fatalf("failed agent: %+v", res)
	}
	tp, d, gate = trialFixture(t, "#!/bin/sh\n[ \"$1\" = --version ] && exit 0\nsleep 30 &\nwait\n", passingTest)
	tp.Task.AgentTimeoutSec = 0.3
	gate()
	started := time.Now()
	res = runTrial(context.Background(), tp, d)
	if !res.AgentTimedOut || res.Reward == nil || *res.Reward != 0 || time.Since(started) > 10*time.Second {
		t.Fatalf("timed-out agent: %+v after %s", res, time.Since(started))
	}
}

func TestRunTrialRefusesTestsThatAreNotLocked(t *testing.T) {
	tp, d, gate := trialFixture(t, fakeAgent, passingTest)
	tp.Task.Files = hex.EncodeToString(sha256.New().Sum(nil))
	gate()
	res := runTrial(context.Background(), tp, d)
	if res.Reward != nil || !strings.Contains(res.Error, "the lock pins") {
		t.Fatalf("result %+v", res)
	}
	if _, err := os.Stat(filepath.Join(d.tests, "test.sh")); err == nil {
		t.Error("unlocked tests were installed")
	}
}

func TestFetchModelChecksEveryFile(t *testing.T) {
	files := map[string]string{"config.json": "{}", "shard/model.safetensors": strings.Repeat("w", 4096)}
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		const prefix = "/org/name/resolve/" + "0123456789012345678901234567890123456789/"
		body, ok := files[strings.TrimPrefix(r.URL.Path, prefix)]
		if !ok || !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	m := modelLock{Engine: engineVLLM, Repo: "org/name", Revision: "0123456789012345678901234567890123456789"}
	for _, p := range slices.Sorted(func(yield func(string) bool) {
		for p := range files {
			if !yield(p) {
				return
			}
		}
	}) {
		sum := sha256.Sum256([]byte(files[p]))
		m.Files = append(m.Files, modelFile{Path: p, Size: int64(len(files[p])), SHA256: hex.EncodeToString(sum[:])})
	}
	dir := t.TempDir()
	var progress bytes.Buffer
	if err := fetchModel(context.Background(), srv.Client(), srv.URL, dir, m, "pin-sum", &progress); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, verifiedMarker)); err != nil || string(data) != "pin-sum\n" {
		t.Fatalf("verified marker %q, %v: want the pin's digest", data, err)
	}
	requests = nil
	if err := fetchModel(context.Background(), srv.Client(), srv.URL, dir, m, "pin-sum", &progress); err != nil || len(requests) != 0 {
		t.Fatalf("second fetch: %v, %d requests", err, len(requests))
	}
	// A kept file of the pinned size but other bytes (a torn or tampered copy) is fetched again.
	shard := filepath.Join(dir, "shard", "model.safetensors")
	if err := os.WriteFile(shard, []byte(strings.Repeat("v", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	requests = nil
	if err := fetchModel(context.Background(), srv.Client(), srv.URL, dir, m, "pin-sum", &progress); err != nil || len(requests) != 1 {
		t.Fatalf("refetch of a changed file: %v, %d requests", err, len(requests))
	}
	if data, _ := os.ReadFile(shard); string(data) != files["shard/model.safetensors"] {
		t.Fatal("the changed file was kept")
	}
	m.Files[0].SHA256 = stagedSum
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := fetchModel(context.Background(), srv.Client(), srv.URL, dir, m, "pin-sum", &progress); err == nil {
		t.Fatal("a file with the wrong sha256 was accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, verifiedMarker)); err == nil {
		t.Fatal("the marker survived a failed fetch")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
		t.Fatal("a file with the wrong sha256 was kept")
	}
}

// An agent shares the trial container, so it can write the gate's request itself. The gate answers only once, into a
// directory the trial container mounts read-only: an answer before the trial's own request is the agent's, and the trial
// is not scored, even when the agent cleans up what it can and leaves a process behind to answer the trial's request.
func TestRunTrialRefusesAnAgentThatAsksTheGate(t *testing.T) {
	const cheat = `#!/bin/sh
[ "$1" = --version ] && exit 0
printf run > "$FAKE_REQUEST/agent-done"
while [ ! -f "$FAKE_ANSWER/answer" ]; do sleep 0.05; done
grep -q x "$FAKE_ANSWER/tests/data/x.txt" && echo answer > "$FAKE_WORK"
rm -f "$FAKE_REQUEST/agent-done"
`
	tp, d, gate := trialFixture(t, cheat, passingTest)
	gate()
	res := runTrial(context.Background(), tp, d)
	if res.Reward != nil || !strings.Contains(res.Error, "the agent asked the verifier gate for the tests") {
		t.Fatalf("an agent that read the tests: %+v", res)
	}
	// A request the agent writes as the trial ends is answered with the agent's request, not the trial's nonce.
	tp, d, _ = trialFixture(t, fakeAgent, passingTest)
	go func() {
		for {
			if data, err := os.ReadFile(filepath.Join(d.request, "agent-done")); err == nil && len(data) > 0 {
				_ = os.WriteFile(filepath.Join(d.answer, "answer"), []byte("run 00000000000000000000000000000000\n"), 0o644)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	res = runTrial(context.Background(), tp, d)
	if res.Reward != nil || !strings.Contains(res.Error, "another request than the trial's") {
		t.Fatalf("an answer without the trial's nonce: %+v", res)
	}
	tp, d, _ = trialFixture(t, fakeAgent, passingTest)
	if err := os.WriteFile(filepath.Join(d.answer, "answer"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	res = runTrial(context.Background(), tp, d)
	if res.Reward != nil || res.AgentExit != -1 || !strings.Contains(res.Error, "before the agent ran") {
		t.Fatalf("stale answer: %+v", res)
	}
}

// The prepare init container gives the trial container the arm's runtime and the instruction, and nothing else of the
// task: the tests stay on the claim.
func TestPrepareCopiesTheArmAndTheInstructionOnly(t *testing.T) {
	for _, arm := range []string{armPig, armPi} {
		base := t.TempDir()
		bundle, root := filepath.Join(base, "bundle"), filepath.Join(base, "root")
		writeTask(t, filepath.Join(bundle, "tasks", "one"), "#!/bin/sh\n")
		for _, f := range []string{"bin/k8sbench", "bin/pig", nodeBinary, piAgentCLI} {
			p := filepath.Join(bundle, filepath.FromSlash(f))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(f), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command("sh", "-c", prepareScript)
		cmd.Env = append(os.Environ(), "K8SBENCH_PREPARE_BUNDLE="+bundle, "K8SBENCH_PREPARE_ROOT="+root, "K8SBENCH_ARM="+arm, "K8SBENCH_TASK=one")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", arm, err, out)
		}
		var got []string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(root, p)
				got = append(got, filepath.ToSlash(rel))
			}
			return err
		})
		want := []string{"bin/k8sbench", "bin/pig", "task/instruction.md"}
		if arm == armPi {
			want = []string{"bin/k8sbench", nodeBinary, piAgentCLI, "task/instruction.md"}
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: the trial root holds %v, want %v", arm, got, want)
		}
	}
}

// An arm whose runtime does not start on the task's image is not scored: its agent would fail for a reason of the
// image, not of the agent.
func TestRunTrialDoesNotScoreAnArmThatDoesNotStart(t *testing.T) {
	tp, d, gate := trialFixture(t, "#!/bin/sh\necho 'node: version GLIBC_2.38 not found' >&2\nexit 1\n", passingTest)
	gate()
	res := runTrial(context.Background(), tp, d)
	if res.Reward != nil || !strings.Contains(res.Error, "does not start on this image") || !strings.Contains(res.Error, "GLIBC") {
		t.Fatalf("result %+v", res)
	}
}

// The agent runs in the trial container, so it can write to the tests directory and the verifier's logs before the
// tests arrive. A reward it planted is not read, and a file it planted beside the tests is not there when they run.
func TestRunTrialIgnoresWhatTheAgentPlantedForTheVerifier(t *testing.T) {
	const planting = `#!/bin/sh
mkdir -p "$FAKE_LOGS/verifier" "$FAKE_LOGS/../tests"
echo 1 > "$FAKE_LOGS/verifier/reward.txt"
echo yes > "$FAKE_LOGS/../tests/planted"
`
	// A test that writes no reward: only a planted one could score it.
	tp, d, gate := trialFixture(t, planting, "#!/bin/bash\nexit 1\n")
	gate()
	res := runTrial(context.Background(), tp, d)
	if res.Reward != nil || !strings.Contains(res.Error, "wrote no reward") {
		t.Fatalf("planted reward: %+v", res)
	}
	// A test that passes only with the planted file.
	tp, d, gate = trialFixture(t, planting, `#!/bin/bash
mkdir -p "$FAKE_LOGS/verifier"
if [ -e "$(dirname "$0")/planted" ]; then echo 1 > "$FAKE_LOGS/verifier/reward.txt"; else echo 0 > "$FAKE_LOGS/verifier/reward.txt"; fi
`)
	gate()
	res = runTrial(context.Background(), tp, d)
	if res.Reward == nil || *res.Reward != 0 || res.Error != "" {
		t.Fatalf("planted test file: %+v", res)
	}
}
