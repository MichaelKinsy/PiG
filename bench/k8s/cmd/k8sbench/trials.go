package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// tasksLock pins a task dataset: the repository at one commit, and for each task its prebuilt image by digest and
// the sha256 digest of the files a trial reads (instruction.md and tests/).
type tasksLock struct {
	Dataset   string     `json:"dataset"`
	Repo      string     `json:"repo"`
	Commit    string     `json:"commit"`
	Published string     `json:"published"`
	Tasks     []taskLock `json:"tasks"`
}

type taskLock struct {
	Name               string  `json:"name"`
	Image              string  `json:"image"`
	Files              string  `json:"files"`
	AgentTimeoutSec    float64 `json:"agentTimeoutSec"`
	VerifierTimeoutSec float64 `json:"verifierTimeoutSec"`
	CPUs               int     `json:"cpus"`
	MemoryMB           int     `json:"memoryMB"`
	StorageMB          int     `json:"storageMB"`
	AllowInternet      bool    `json:"allowInternet"`
}

var taskName = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,61}[a-z0-9])?$`)

func readTasksLock(path string) (*tasksLock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l tasksLock
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&l); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := l.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &l, nil
}

func (l *tasksLock) validate() error {
	if l.Dataset == "" || !strings.HasPrefix(l.Repo, "https://") || !gitCommit.MatchString(l.Commit) {
		return errors.New("needs dataset, an https repo and a 40-hex commit")
	}
	if _, err := time.Parse(time.DateOnly, l.Published); err != nil {
		return fmt.Errorf("published: %w", err)
	}
	if len(l.Tasks) == 0 {
		return errors.New("pins no tasks")
	}
	seen := map[string]bool{}
	for _, t := range l.Tasks {
		if !taskName.MatchString(t.Name) || seen[t.Name] {
			return fmt.Errorf("task name %q is not unique, or not lowercase letters, digits, dots, dashes and underscores (a label value)", t.Name)
		}
		seen[t.Name] = true
		if !pinnedImage.MatchString(t.Image) {
			return fmt.Errorf("task %s: image %q is not pinned by digest", t.Name, t.Image)
		}
		if !sha256Hex.MatchString(t.Files) {
			return fmt.Errorf("task %s: files is not a sha256", t.Name)
		}
		if t.AgentTimeoutSec <= 0 || t.VerifierTimeoutSec <= 0 || t.CPUs <= 0 || t.MemoryMB <= 0 || t.StorageMB <= 0 {
			return fmt.Errorf("task %s: needs positive timeouts, cpus, memoryMB and storageMB", t.Name)
		}
	}
	return nil
}

func (l *tasksLock) find(name string) (taskLock, bool) {
	i := slices.IndexFunc(l.Tasks, func(t taskLock) bool { return t.Name == name })
	if i < 0 {
		return taskLock{}, false
	}
	return l.Tasks[i], true
}

// taskFilesDigest is the sha256 of "<sha256>  <path>\n" over the task's instruction.md and every file under tests/,
// sorted by path. Anything but a regular file or a directory is refused.
func taskFilesDigest(dir string) (string, error) {
	paths := []string{"instruction.md"}
	err := filepath.WalkDir(filepath.Join(dir, "tests"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", err
	}
	slices.Sort(paths)
	h := sha256.New()
	for _, p := range paths {
		sum, err := fileSHA256(filepath.Join(dir, filepath.FromSlash(p)))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(h, "%s  %s\n", sum, p)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// taskToml is the part of a Harbor task.toml a trial needs.
type taskToml struct {
	Agent struct {
		TimeoutSec float64 `toml:"timeout_sec"`
	} `toml:"agent"`
	Verifier struct {
		TimeoutSec float64 `toml:"timeout_sec"`
	} `toml:"verifier"`
	Environment struct {
		DockerImage   string `toml:"docker_image"`
		CPUs          int    `toml:"cpus"`
		MemoryMB      int    `toml:"memory_mb"`
		StorageMB     int    `toml:"storage_mb"`
		GPUs          int    `toml:"gpus"`
		AllowInternet bool   `toml:"allow_internet"`
	} `toml:"environment"`
}

// readTask reads one task directory of a dataset checkout. ok is false for a task the workload cannot run (no
// prebuilt image, or GPUs), with the reason.
func readTask(dir string) (t taskLock, ok bool, reason string, err error) {
	var tt taskToml
	if _, err := toml.DecodeFile(filepath.Join(dir, "task.toml"), &tt); err != nil {
		return t, false, "", err
	}
	t = taskLock{Name: filepath.Base(dir), AgentTimeoutSec: tt.Agent.TimeoutSec, VerifierTimeoutSec: tt.Verifier.TimeoutSec,
		CPUs: tt.Environment.CPUs, MemoryMB: tt.Environment.MemoryMB, StorageMB: tt.Environment.StorageMB, AllowInternet: tt.Environment.AllowInternet}
	switch {
	case tt.Environment.DockerImage == "":
		return t, false, "no prebuilt docker_image", nil
	case tt.Environment.GPUs > 0:
		return t, false, "needs GPUs", nil
	}
	t.Image = tt.Environment.DockerImage
	if t.Files, err = taskFilesDigest(dir); err != nil {
		return t, false, "", err
	}
	return t, true, "", nil
}

// imageDigester resolves an image reference to its registry digest.
type imageDigester func(ctx context.Context, ref string) (string, error)

// registryDigest asks a registry for the digest of ref's manifest (an index for a multi-platform image). Docker Hub
// references get an anonymous pull token first.
func registryDigest(client *http.Client) imageDigester {
	return func(ctx context.Context, ref string) (string, error) {
		host, repo, tag, err := splitImageRef(ref)
		if err != nil {
			return "", err
		}
		token := ""
		if host == "registry-1.docker.io" {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://auth.docker.io/token?service=registry.docker.io&scope=repository:"+repo+":pull", nil)
			if err != nil {
				return "", err
			}
			resp, err := client.Do(req)
			if err != nil {
				return "", err
			}
			var body struct{ Token string }
			err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body)
			_ = resp.Body.Close()
			if err != nil {
				return "", fmt.Errorf("%s: pull token: %w", ref, err)
			}
			token = body.Token
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://"+host+"/v2/"+repo+"/manifests/"+tag, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		_ = resp.Body.Close()
		digest := resp.Header.Get("Docker-Content-Digest")
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(digest, "sha256:") || !sha256Hex.MatchString(strings.TrimPrefix(digest, "sha256:")) {
			return "", fmt.Errorf("%s: registry answered %s with digest %q", ref, resp.Status, digest)
		}
		return digest, nil
	}
}

// splitImageRef splits name:tag into registry host, repository and tag, with Docker Hub's defaults.
func splitImageRef(ref string) (host, repo, tag string, err error) {
	if strings.Contains(ref, "@") {
		return "", "", "", fmt.Errorf("%s is already pinned", ref)
	}
	name := ref
	tag = "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		name, tag = ref[:i], ref[i+1:]
	}
	host = "registry-1.docker.io"
	if first, rest, ok := strings.Cut(name, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		host, name = first, rest
		if host == "docker.io" {
			host = "registry-1.docker.io"
		}
	}
	if host == "registry-1.docker.io" && !strings.Contains(name, "/") {
		name = "library/" + name
	}
	if name == "" || tag == "" {
		return "", "", "", fmt.Errorf("%q is not an image reference", ref)
	}
	return host, name, tag, nil
}

// lockTasks reads every task of a dataset checkout and pins its image. Tasks the workload cannot run are reported on
// progress and left out.
func lockTasks(ctx context.Context, src string, base tasksLock, digest imageDigester, progress io.Writer) (*tasksLock, error) {
	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, err
	}
	l := base
	l.Tasks = nil
	for _, e := range entries {
		dir := filepath.Join(src, e.Name())
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "task.toml")); err != nil {
			continue
		}
		t, ok, reason, err := readTask(dir)
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", e.Name(), err)
		}
		if !ok {
			_, _ = fmt.Fprintf(progress, "k8sbench tasks-lock: skip %s: %s\n", t.Name, reason)
			continue
		}
		d, err := digest(ctx, t.Image)
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", t.Name, err)
		}
		if host, _, _, _ := splitImageRef(t.Image); host == "registry-1.docker.io" && !strings.HasPrefix(t.Image, "docker.io/") {
			t.Image = "docker.io/" + t.Image
		}
		t.Image += "@" + d
		l.Tasks = append(l.Tasks, t)
	}
	return &l, l.validate()
}

// cmdTasksLock writes tasks.lock from a checkout of the dataset at the pinned commit.
func cmdTasksLock(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("tasks-lock", flag.ContinueOnError)
	fs.SetOutput(stderr)
	src := fs.String("src", "", "the dataset's tasks directory, checked out at -commit")
	dataset := fs.String("dataset", "", "dataset name")
	repo := fs.String("repo-url", "", "dataset repository (https)")
	commit := fs.String("commit", "", "dataset commit (40 hex)")
	published := fs.String("published", "", "commit date, YYYY-MM-DD (at least seven days old)")
	out := fs.String("out", tasksLockFile, "output file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if p, err := time.Parse(time.DateOnly, *published); err != nil || time.Since(p) < minPinAge {
		return fmt.Errorf("-published %q must be a date at least seven days old", *published)
	}
	l, err := lockTasks(ctx, *src, tasksLock{Dataset: *dataset, Repo: *repo, Commit: *commit, Published: *published},
		registryDigest(&http.Client{Timeout: time.Minute}), stderr)
	if err != nil {
		return err
	}
	//portlint:allow jsonescape benchmark tool output, not Pi-facing
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, append(data, '\n'), 0o644)
}

// addTasks copies each locked task's instruction.md and tests/ from a dataset checkout into the bundle's tasks/,
// refusing a task whose files do not match the lock.
func addTasks(src, outDir string, l *tasksLock) error {
	for _, t := range l.Tasks {
		dir := filepath.Join(src, t.Name)
		got, err := taskFilesDigest(dir)
		if err != nil {
			return fmt.Errorf("task %s: %w", t.Name, err)
		}
		if got != t.Files {
			return fmt.Errorf("task %s: files digest %s, the lock pins %s (check out commit %s)", t.Name, got, t.Files, l.Commit)
		}
		dst := filepath.Join(outDir, "tasks", t.Name)
		err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			if rel != "." && rel != "instruction.md" && rel != "tests" && !strings.HasPrefix(rel, "tests"+string(filepath.Separator)) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
		})
		if err != nil {
			return err
		}
	}
	//portlint:allow jsonescape benchmark tool output, not Pi-facing
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "tasks", "tasks.lock"), append(data, '\n'), 0o644)
}

// taskArtifacts lists the bundle's task files for ARTIFACTS.sha256.
func taskArtifacts(dir string) ([]string, error) {
	root := filepath.Join(dir, "tasks")
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		paths = append(paths, filepath.ToSlash(rel))
		return err
	})
	slices.Sort(paths)
	return paths, err
}

// Trial arms: the bundle's own PiG (bin/pig) and the bundle's pinned Pi (pi-agent/, run by the bundle's node/).
const (
	armPig = "pig"
	armPi  = "pi"
)

// trialPlan is what one trial pod runs (K8SBENCH_TRIAL).
type trialPlan struct {
	Task          taskLock `json:"task"`
	Arm           string   `json:"arm"`
	Attempt       int      `json:"attempt"`
	Model         string   `json:"model"`
	BaseURL       string   `json:"baseUrl"`
	ContextWindow int      `json:"contextWindow"`
	MaxTokens     int      `json:"maxTokens"`
	// TestsTimeoutSeconds bounds the wait for the verifier gate's copy of the tests.
	TestsTimeoutSeconds int `json:"testsTimeoutSeconds"`
}

// stageScript unpacks a delivered bundle onto the trial claim as bundles/<sha256>, checking the archive's sha256 and
// every artifact first. A bundle already staged is kept.
const stageScript = `set -eu
b=/bundle
fail() { printf '{"t":"error","message":"%s"}\n' "$1"; exit 1; }
dst="` + stageMount + `/bundles/$K8SBENCH_BUNDLE_SHA256"
[ -d "$dst" ] && { printf '{"t":"staged","sha256":"%s","existing":true}\n' "$K8SBENCH_BUNDLE_SHA256"; exit 0; }
end=$(( $(date +%s) + ${K8SBENCH_DELIVERY_TIMEOUT:-1800} ))
while [ ! -f "$b/READY" ]; do
	[ "$(date +%s)" -lt "$end" ] || fail "no bundle arrived"
	sleep 1
done
echo "$K8SBENCH_BUNDLE_SHA256  $b/bundle.tar.gz" | sha256sum -c --status - || fail "bundle.tar.gz does not have the sha256 the runner sent"
rm -rf "$dst.tmp"
mkdir -p "$dst.tmp"
tar -xzf "$b/bundle.tar.gz" -C "$dst.tmp" --no-same-owner || fail "bundle.tar.gz does not unpack"
(cd "$dst.tmp" && sha256sum -c --status --strict ARTIFACTS.sha256) || fail "an artifact does not match ARTIFACTS.sha256"
[ -f "$dst.tmp/tasks/tasks.lock" ] || fail "the bundle carries no tasks (k8sbench bundle -agents -tasks DIR)"
[ -x "$dst.tmp/node/bin/node" ] || fail "the bundle carries no node (k8sbench bundle -agents)"
mv "$dst.tmp" "$dst"
printf '{"t":"staged","sha256":"%s"}\n' "$K8SBENCH_BUNDLE_SHA256"
`

// renderStage renders the Job that puts a bundle on the trial claim; the operator copies bundle.tar.gz and then
// READY into its pod.
func renderStage(v values, g gpuValues, sum string) []k8sObject {
	labels := gpuLabels("stage", sum[:12])
	pod := map[string]any{
		"restartPolicy": "Never",
		"containers": []any{map[string]any{
			"name": "bench", "image": baseImageOf(v), "command": []string{"sh", "-c", stageScript},
			"env":          []any{map[string]any{"name": "K8SBENCH_BUNDLE_SHA256", "value": sum}},
			"resources":    resources(1, "2Gi", nil),
			"volumeMounts": []any{mount("stage", stageMount, false), mount("incoming", "/bundle", false)},
		}},
		"volumes": []any{pvcVolume("stage", g.TrialPVC, false),
			map[string]any{"name": "incoming", "emptyDir": map[string]any{"sizeLimit": "8Gi"}}},
	}
	podCommon(pod, v, v.NodeSelector, v.Tolerations)
	return []k8sObject{job("k8sbench-stage-"+sum[:12], labels, 3600, v.TTLSeconds, pod)}
}

// prepareScript copies what one trial needs from the staged bundle into the pod's own volume: the arm's runtime and
// the task's instruction, never its tests. The K8SBENCH_PREPARE_* variables point the script at other directories in
// tests.
const prepareScript = `set -eu
b=${K8SBENCH_PREPARE_BUNDLE:-"` + stageMount + `/bundles/$K8SBENCH_STAGED"}
r=${K8SBENCH_PREPARE_ROOT:-` + trialRoot + `}
mkdir -p "$r/bin" "$r/task"
cp "$b/bin/k8sbench" "$r/bin/"
case "$K8SBENCH_ARM" in
pig) cp "$b/bin/pig" "$r/bin/" ;;
pi) cp -a "$b/node" "$b/pi-agent" "$r/" ;;
*) echo "unknown arm $K8SBENCH_ARM" >&2; exit 1 ;;
esac
cp "$b/tasks/$K8SBENCH_TASK/instruction.md" "$r/task/"
`

// gateScript hands the task's tests to the trial container only after the agent has exited, as Harbor copies tests in
// after the agent run; the trial container never mounts the claim that holds them. It answers the first request only,
// in the answer directory the trial container mounts read-only, echoing the request (and its nonce) last. The
// K8SBENCH_GATE_* variables point the script at other directories in tests.
const gateScript = `set -eu
q=${K8SBENCH_GATE_REQUEST:-` + requestDir + `}
a=${K8SBENCH_GATE_ANSWER:-` + answerDir + `}
src=${K8SBENCH_GATE_TESTS:-` + stageMount + `/bundles/$K8SBENCH_STAGED/tasks/$K8SBENCH_TASK/tests}
while [ ! -s "$q/agent-done" ]; do sleep 1; done
request=$(cat "$q/agent-done")
case "$request" in
run\ *) cp -r "$src" "$a/tests.tmp" && mv "$a/tests.tmp" "$a/tests" ;;
esac
printf '%s\n' "$request" > "$a/answer.tmp"
mv "$a/answer.tmp" "$a/answer"
`

func trialJobName(task, arm string, attempt int, staged, model string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{task, arm, strconv.Itoa(attempt), staged, model}, "\x00")))
	return "k8sbench-trial-" + hex.EncodeToString(sum[:])[:16]
}

// renderTrials renders one Job per task, arm and attempt. Each pod runs the task's own image; the model server is
// reached through its Service.
func renderTrials(v values, g gpuValues, ml *modelsLock, tl *tasksLock, model, staged string, arms, tasks []string, attempts int) ([]k8sObject, error) {
	m, ok := ml.Models[model]
	if !ok {
		return nil, fmt.Errorf("model %q is not in %s", model, modelsLockFile)
	}
	if !sha256Hex.MatchString(staged) {
		return nil, fmt.Errorf("-staged %q is not the sha256 of a staged bundle", staged)
	}
	if attempts < 1 {
		return nil, errors.New("-attempts must be at least 1")
	}
	for _, a := range arms {
		if a != armPig && a != armPi {
			return nil, fmt.Errorf("arm %q is not %s or %s", a, armPig, armPi)
		}
	}
	selected := tl.Tasks
	if len(tasks) > 0 {
		selected = nil
		for _, name := range tasks {
			t, ok := tl.find(name)
			if !ok {
				return nil, fmt.Errorf("task %q is not in %s", name, tasksLockFile)
			}
			selected = append(selected, t)
		}
	}
	var out []k8sObject
	for attempt := 1; attempt <= attempts; attempt++ {
		for _, t := range selected {
			for _, arm := range arms {
				j, err := trialJob(v, g, m, t, model, staged, arm, attempt)
				if err != nil {
					return nil, err
				}
				out = append(out, j)
			}
		}
	}
	return out, nil
}

func trialJob(v values, g gpuValues, m modelLock, t taskLock, model, staged, arm string, attempt int) (k8sObject, error) {
	name := trialJobName(t.Name, arm, attempt, staged, model)
	labels := gpuLabels("trial", name)
	labels["k8sbench/model"] = model
	labels["k8sbench/task"] = t.Name
	labels["k8sbench/arm"] = arm
	labels["k8sbench/attempt"] = strconv.Itoa(attempt)
	tp := trialPlan{Task: t, Arm: arm, Attempt: attempt, Model: model, BaseURL: modelURL(model, v.Namespace),
		ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens, TestsTimeoutSeconds: 600}
	//portlint:allow jsonescape benchmark tool output, not Pi-facing
	plan, err := json.Marshal(tp)
	if err != nil {
		return nil, err
	}
	env := []any{
		map[string]any{"name": "K8SBENCH_STAGED", "value": staged},
		map[string]any{"name": "K8SBENCH_TASK", "value": t.Name},
		map[string]any{"name": "K8SBENCH_ARM", "value": arm},
	}
	emptyDir := func(name, size string) map[string]any {
		return map[string]any{"name": name, "emptyDir": map[string]any{"sizeLimit": size}}
	}
	memory := strconv.Itoa(t.MemoryMB) + "Mi"
	pod := map[string]any{
		"restartPolicy": "Never",
		"initContainers": []any{map[string]any{
			"name": "prepare", "image": baseImageOf(v), "command": []string{"sh", "-c", prepareScript}, "env": env,
			"resources":    resources(1, "1Gi", nil),
			"volumeMounts": []any{mount("stage", stageMount, true), mount("root", trialRoot, false)},
		}},
		"containers": []any{
			map[string]any{
				"name": "bench", "image": t.Image, "command": []string{trialRoot + "/bin/k8sbench", "trial"},
				"env":       []any{map[string]any{"name": "K8SBENCH_TRIAL", "value": string(plan)}},
				"resources": resources(t.CPUs, memory, nil),
				"volumeMounts": []any{mount("root", trialRoot, false), mount("request", requestDir, false),
					mount("answer", answerDir, true), mount("logs", logsDir, false)},
			},
			map[string]any{
				"name": "verifier-gate", "image": baseImageOf(v), "command": []string{"sh", "-c", gateScript}, "env": env,
				"resources":    resources(1, "256Mi", nil),
				"volumeMounts": []any{mount("stage", stageMount, true), mount("request", requestDir, true), mount("answer", answerDir, false)},
			},
		},
		"volumes": []any{pvcVolume("stage", g.TrialPVC, true), emptyDir("root", "4Gi"), emptyDir("request", "1Mi"), emptyDir("answer", "1Gi"),
			emptyDir("logs", strconv.Itoa(t.StorageMB)+"Mi")},
	}
	podCommon(pod, v, v.NodeSelector, v.Tolerations)
	deadline := int(t.AgentTimeoutSec+t.VerifierTimeoutSec) + 1800
	return job(name, labels, deadline, v.TTLSeconds, pod), nil
}

// cmdStage packs a trial bundle and renders the Job that stages it on the trial claim. The archive's sha256 is what
// model and trials take as -staged.
func cmdStage(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("stage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	gf := addGPUFlags(fs)
	tarPath := fs.String("tar", "", "where to write the packed bundle (default <bundle>.tar.gz)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	v, g, err := gf.resolve(getenv, false, true)
	if err != nil {
		return err
	}
	if v.Bundle == "" {
		return errors.New("stage needs -bundle DIR (k8sbench bundle -agents -tasks ...)")
	}
	if _, err := inspectBundle(v.Bundle); err != nil {
		return err
	}
	for _, need := range []string{"tasks/tasks.lock", nodeBinary, "bin/pig", piAgentCLI} {
		if _, err := os.Stat(filepath.Join(v.Bundle, filepath.FromSlash(need))); err != nil {
			return fmt.Errorf("the bundle has no %s: build it with k8sbench bundle -agents -tasks DIR", need)
		}
	}
	out := *tarPath
	if out == "" {
		out = strings.TrimSuffix(filepath.Clean(v.Bundle), string(filepath.Separator)) + ".tar.gz"
	}
	sum, size, err := packBundle(v.Bundle, out)
	if err != nil {
		return err
	}
	objects := renderStage(v, g, sum)
	name := "k8sbench-stage-" + sum[:12]
	_, _ = fmt.Fprintf(stderr, `k8sbench stage: %s (%d bytes), sha256 %s
  kubectl apply -f <this output>
  kubectl wait --for=jsonpath='{.status.phase}'=Running pod -l job-name=%[4]s
  kubectl cp %[1]s <pod>:/bundle/bundle.tar.gz -c bench && kubectl cp <empty file> <pod>:/bundle/READY -c bench
  kubectl wait --for=condition=complete job/%[4]s
then pass -staged %[3]s to k8sbench model and k8sbench trials
`, out, size, sum, name)
	return writeObjects(stdout, objects)
}

// cmdTrials renders one Job per task, arm and attempt.
func cmdTrials(args []string, stdout, stderr io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("trials", flag.ContinueOnError)
	fs.SetOutput(stderr)
	gf := addGPUFlags(fs)
	model := fs.String("model", "", "model id in "+modelsLockFile+" (its server must be up)")
	staged := fs.String("staged", "", "sha256 of the staged bundle (k8sbench stage prints it)")
	armsFlag := fs.String("trial-arms", armPig+","+armPi, "comma-separated arms: pig, pi")
	attempts := fs.Int("attempts", 5, "attempts per task and arm")
	tasks := fs.String("tasks", "", "comma-separated task names (default: every task in "+tasksLockFile+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	v, g, err := gf.resolve(getenv, false, true)
	if err != nil {
		return err
	}
	ml, err := readModelsLock(filepath.Join(*gf.repo, modelsLockFile))
	if err != nil {
		return err
	}
	tl, err := readTasksLock(filepath.Join(*gf.repo, tasksLockFile))
	if err != nil {
		return err
	}
	var names []string
	if *tasks != "" {
		names = strings.Split(*tasks, ",")
	}
	objects, err := renderTrials(v, g, ml, tl, *model, *staged, strings.Split(*armsFlag, ","), names, *attempts)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, "k8sbench trials: %d Jobs; each prints one {\"t\":\"trial\"} line (kubectl logs job/<name> -c bench)\n", len(objects))
	return writeObjects(stdout, objects)
}
