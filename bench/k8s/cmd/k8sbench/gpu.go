package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// The GPU task-completion workload: one model server (vLLM or llama.cpp, image pinned by digest, weights pinned by
// sha256) and one Job per trial (task, arm, attempt) whose image is the task's own prebuilt image. The runner only
// renders the manifests; applying them is the operator's step.

// models.lock pins the serving engines and the model weights; tasks.lock pins the task dataset.
const (
	modelsLockFile = "bench/k8s/models.lock"
	tasksLockFile  = "bench/k8s/tasks.lock"
)

// Serving engines.
const (
	engineVLLM     = "vllm"
	engineLlamaCPP = "llama.cpp"
)

// Mount points of the GPU workload's pods.
const (
	modelsMount = "/models"
	stageMount  = "/k8sbench-stage"
	trialRoot   = "/k8sbench"
	// The verifier gate's channel: the trial container asks in requestDir and reads the answer in answerDir, which it
	// mounts read-only, so an agent can ask early but cannot forge, hide or undo the gate's answer.
	requestDir = "/handoff/request"
	answerDir  = "/handoff/answer"
	logsDir    = "/logs"
	serverPort = 8000
)

type modelsLock struct {
	Engines map[string]engineLock `json:"engines"`
	Models  map[string]modelLock  `json:"models"`
}

type engineLock struct {
	Image     string `json:"image"`
	Published string `json:"published"`
}

// modelLock pins one model: a Hugging Face repository at one commit and every file the server reads, by size and
// sha256. ContextWindow is the context the server is started with and the agents are told; Args are the engine's
// model-specific arguments (a tool-call parser, a chat-template switch).
type modelLock struct {
	Engine        string `json:"engine"`
	Repo          string `json:"repo"`
	Revision      string `json:"revision"`
	Published     string `json:"published"`
	License       string `json:"license"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
	//portlint:allow emptydrop benchmark tool output, not Pi-facing
	Args  []string    `json:"args,omitempty"`
	Files []modelFile `json:"files"`
}

type modelFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

var (
	gitCommit   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	hfRepo      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)
	lockID      = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)
	pinnedImage = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
)

// minPinAge is how old a release must be when it is pinned.
const minPinAge = 7 * 24 * time.Hour

func readModelsLock(path string) (*modelsLock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var l modelsLock
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

func (l *modelsLock) validate() error {
	for _, name := range slices.Sorted(maps.Keys(l.Engines)) {
		e := l.Engines[name]
		if name != engineVLLM && name != engineLlamaCPP {
			return fmt.Errorf("engine %q is not %s or %s", name, engineVLLM, engineLlamaCPP)
		}
		if !pinnedImage.MatchString(e.Image) {
			return fmt.Errorf("engine %s: image %q is not pinned by digest", name, e.Image)
		}
		if _, err := time.Parse(time.DateOnly, e.Published); err != nil {
			return fmt.Errorf("engine %s: published: %w", name, err)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(l.Models)) {
		if err := l.Models[id].validate(id, l.Engines); err != nil {
			return err
		}
	}
	return nil
}

func (m modelLock) validate(id string, engines map[string]engineLock) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("model %s: "+format, append([]any{id}, args...)...)
	}
	if !lockID.MatchString(id) || len(id) > 40 {
		return fail("the id must be lowercase letters, digits, dots and dashes, at most 40 characters")
	}
	if _, ok := engines[m.Engine]; !ok {
		return fail("engine %q is not in the lock", m.Engine)
	}
	if !hfRepo.MatchString(m.Repo) || !gitCommit.MatchString(m.Revision) {
		return fail("needs repo owner/name and a 40-hex revision")
	}
	if _, err := time.Parse(time.DateOnly, m.Published); err != nil {
		return fail("published: %w", err)
	}
	if m.ContextWindow <= 0 || m.MaxTokens <= 0 || m.MaxTokens >= m.ContextWindow {
		return fail("needs contextWindow > maxTokens > 0")
	}
	if len(m.Files) == 0 {
		return fail("pins no files")
	}
	seen := map[string]bool{}
	ggufs := 0
	for _, f := range m.Files {
		//portlint:allow pathseparators f.Path is a slash-separated Hugging Face repo path that becomes a /resolve/ URL, not a host path
		if f.Path == "" || path.Clean(f.Path) != f.Path || path.IsAbs(f.Path) || strings.HasPrefix(f.Path, "..") || seen[f.Path] {
			return fail("file path %q is not a clean, unique relative path", f.Path)
		}
		seen[f.Path] = true
		if f.Size <= 0 || !sha256Hex.MatchString(f.SHA256) {
			return fail("file %s needs a size and a sha256", f.Path)
		}
		if strings.HasSuffix(f.Path, ".gguf") {
			ggufs++
		}
	}
	if m.Engine == engineLlamaCPP && ggufs != 1 {
		return fail("llama.cpp serves exactly one .gguf file; the lock pins %d", ggufs)
	}
	return nil
}

// checkPinAges refuses a pin younger than minPinAge at now.
func (l *modelsLock) checkPinAges(now time.Time) error {
	check := func(what, published string) error {
		t, err := time.Parse(time.DateOnly, published)
		if err != nil {
			return err
		}
		if now.Sub(t) < minPinAge {
			return fmt.Errorf("%s was published %s, less than seven days before %s", what, published, now.UTC().Format(time.DateOnly))
		}
		return nil
	}
	for _, name := range slices.Sorted(maps.Keys(l.Engines)) {
		if err := check("engine "+name, l.Engines[name].Published); err != nil {
			return err
		}
	}
	for _, id := range slices.Sorted(maps.Keys(l.Models)) {
		if err := check("model "+id, l.Models[id].Published); err != nil {
			return err
		}
	}
	return nil
}

// gpuValues are the GPU workload's cluster values (the values file's gpu: section).
type gpuValues struct {
	// Resource is the extended resource the server requests, for example nvidia.com/gpu or amd.com/gpu.
	Resource     string            `yaml:"resource"`
	Count        int               `yaml:"count"`
	NodeSelector map[string]string `yaml:"nodeSelector"`
	Tolerations  []toleration      `yaml:"tolerations"`
	ServerCPUs   int               `yaml:"serverCpus"`
	ServerMemory string            `yaml:"serverMemory"`
	// ModelPVC holds the weights (written by the fetch Job, read by the server); TrialPVC holds the staged bundle
	// and tasks (written by the stage Job, read by every trial). Both are existing claims.
	ModelPVC string `yaml:"modelPVC"`
	TrialPVC string `yaml:"trialPVC"`
}

func (g gpuValues) withDefaults() gpuValues {
	if g.Resource == "" {
		g.Resource = "nvidia.com/gpu"
	}
	if g.Count == 0 {
		g.Count = 1
	}
	if g.ServerCPUs == 0 {
		g.ServerCPUs = 8
	}
	if g.ServerMemory == "" {
		g.ServerMemory = "64Gi"
	}
	return g
}

var extendedResource = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?/[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

func (g gpuValues) validate(needModel, needTrial bool) error {
	var problems []string
	if !extendedResource.MatchString(g.Resource) {
		problems = append(problems, fmt.Sprintf("gpu.resource %q is not an extended resource name such as nvidia.com/gpu", g.Resource))
	}
	if g.Count < 1 {
		problems = append(problems, "gpu.count must be at least 1")
	}
	if needModel && g.ModelPVC == "" {
		problems = append(problems, "gpu.modelPVC is not set (-model-pvc or the values file)")
	}
	if needTrial && g.TrialPVC == "" {
		problems = append(problems, "gpu.trialPVC is not set (-trial-pvc or the values file)")
	}
	for _, s := range []string{g.ModelPVC, g.TrialPVC} {
		if placeholder.MatchString(s) {
			problems = append(problems, fmt.Sprintf("%q is still a placeholder", s))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// k8sObject is one rendered manifest.
type k8sObject = map[string]any

func meta(name string, labels map[string]string) map[string]any {
	return map[string]any{"name": name, "labels": labels}
}

func gpuLabels(role, id string) map[string]string {
	return map[string]string{"app.kubernetes.io/name": "k8sbench", "k8sbench/role": role, "k8sbench/id": id}
}

func tolerationList(ts []toleration) []any {
	out := make([]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, t)
	}
	return out
}

// podCommon fills the pod-spec fields every GPU-workload pod shares. No pod gets an API token or service environment
// variables: a trial pod runs the agent's commands, and none of the pods calls the API.
func podCommon(spec map[string]any, v values, selector map[string]string, tolerations []toleration) {
	spec["automountServiceAccountToken"] = false
	spec["enableServiceLinks"] = false
	if v.ServiceAccountName != "" {
		spec["serviceAccountName"] = v.ServiceAccountName
	}
	if len(v.ImagePullSecrets) > 0 {
		var secrets []any
		for _, s := range v.ImagePullSecrets {
			secrets = append(secrets, map[string]any{"name": s})
		}
		spec["imagePullSecrets"] = secrets
	}
	if len(selector) > 0 {
		spec["nodeSelector"] = selector
	}
	if len(tolerations) > 0 {
		spec["tolerations"] = tolerationList(tolerations)
	}
}

func resources(cpus int, memory string, extra map[string]string) map[string]any {
	r := map[string]any{"cpu": strconv.Itoa(cpus), "memory": memory}
	for k, val := range extra {
		r[k] = val
	}
	return map[string]any{"requests": r, "limits": r}
}

func job(name string, labels map[string]string, deadline, ttl int, pod map[string]any) k8sObject {
	return k8sObject{
		"apiVersion": "batch/v1", "kind": "Job", "metadata": meta(name, labels),
		"spec": map[string]any{
			"backoffLimit": 0, "activeDeadlineSeconds": deadline, "ttlSecondsAfterFinished": ttl,
			"template": map[string]any{"metadata": map[string]any{"labels": labels}, "spec": pod},
		},
	}
}

func modelName(id string) string { return "k8sbench-model-" + id }

// modelURL is the in-cluster OpenAI-compatible base URL of a model's Service.
func modelURL(id, namespace string) string {
	return fmt.Sprintf("http://%s.%s.svc:%d/v1", strings.ReplaceAll(modelName(id), ".", "-"), namespace, serverPort)
}

// renderModel renders the weights fetch Job, the server Deployment and its Service for one locked model.
func renderModel(v values, g gpuValues, l *modelsLock, id, stagedSHA string) ([]k8sObject, error) {
	m, ok := l.Models[id]
	if !ok {
		return nil, fmt.Errorf("model %q is not in %s (have %s)", id, modelsLockFile, strings.Join(slices.Sorted(maps.Keys(l.Models)), ", "))
	}
	if !sha256Hex.MatchString(stagedSHA) {
		return nil, fmt.Errorf("-staged %q is not the sha256 of a staged bundle", stagedSHA)
	}
	name := strings.ReplaceAll(modelName(id), ".", "-")
	//portlint:allow jsonescape the bytes are the model pin K8SBENCH_MODEL carries and pinDigest hashes, read back by Go json; Go-only bench tool
	pin, err := json.Marshal(struct {
		ID string `json:"id"`
		modelLock
	}{id, m})
	if err != nil {
		return nil, err
	}
	pinSum := pinDigest(string(pin))
	fetchLabels := gpuLabels("model-fetch", id)
	fetchPod := map[string]any{
		"restartPolicy": "Never",
		"containers": []any{map[string]any{
			"name": "bench", "image": baseImageOf(v),
			"command": []string{stageMount + "/bundles/" + stagedSHA + "/bin/k8sbench", "model-fetch"},
			"env": []any{
				map[string]any{"name": "K8SBENCH_MODEL", "value": string(pin)},
				map[string]any{"name": "K8SBENCH_MODELS_DIR", "value": modelsMount},
			},
			"resources":    resources(1, "2Gi", nil),
			"volumeMounts": []any{mount("models", modelsMount, false), mount("stage", stageMount, true)},
		}},
		"volumes": []any{pvcVolume("models", g.ModelPVC, false), pvcVolume("stage", g.TrialPVC, true)},
	}
	podCommon(fetchPod, v, v.NodeSelector, v.Tolerations)

	served := modelsMount + "/" + id
	var command []string
	switch m.Engine {
	case engineVLLM:
		command = []string{"vllm", "serve", served, "--served-model-name", id, "--host", "0.0.0.0", "--port", strconv.Itoa(serverPort),
			"--max-model-len", strconv.Itoa(m.ContextWindow), "--seed", "0", "--tensor-parallel-size", strconv.Itoa(g.Count)}
	case engineLlamaCPP:
		var gguf string
		for _, f := range m.Files {
			if strings.HasSuffix(f.Path, ".gguf") {
				gguf = f.Path
			}
		}
		command = []string{"/app/llama-server", "-m", served + "/" + gguf, "--alias", id, "--host", "0.0.0.0", "--port", strconv.Itoa(serverPort),
			"-c", strconv.Itoa(m.ContextWindow), "-ngl", "999", "--seed", "0"}
	}
	command = append(command, m.Args...)
	// The server starts only on weights the fetch Job verified for this pin, and never reaches the network for them.
	script := fmt.Sprintf(`set -eu
[ "$(cat %[1]s/%[3]s 2>/dev/null)" = %[4]s ] || { echo "weights of %[2]s are not verified for this pin: run its fetch Job first" >&2; exit 1; }
exec "$@"`, served, id, verifiedMarker, pinSum)
	serverLabels := gpuLabels("model-server", id)
	serverPod := map[string]any{
		"containers": []any{map[string]any{
			"name": "server", "image": l.Engines[m.Engine].Image,
			"command": append([]string{"sh", "-c", script, "server"}, command...),
			"env": []any{
				map[string]any{"name": "HF_HUB_OFFLINE", "value": "1"},
				map[string]any{"name": "VLLM_NO_USAGE_STATS", "value": "1"},
				map[string]any{"name": "DO_NOT_TRACK", "value": "1"},
			},
			"ports":     []any{map[string]any{"name": "http", "containerPort": serverPort}},
			"resources": resources(g.ServerCPUs, g.ServerMemory, map[string]string{g.Resource: strconv.Itoa(g.Count)}),
			"startupProbe": map[string]any{"httpGet": map[string]any{"path": "/health", "port": serverPort},
				"periodSeconds": 10, "failureThreshold": 180},
			"readinessProbe": map[string]any{"httpGet": map[string]any{"path": "/health", "port": serverPort}, "periodSeconds": 10},
			"volumeMounts":   []any{mount("models", modelsMount, true), map[string]any{"name": "shm", "mountPath": "/dev/shm"}},
		}},
		"volumes": []any{pvcVolume("models", g.ModelPVC, true),
			map[string]any{"name": "shm", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": "16Gi"}}},
	}
	podCommon(serverPod, v, g.NodeSelector, g.Tolerations)
	deployment := k8sObject{
		"apiVersion": "apps/v1", "kind": "Deployment", "metadata": meta(name, serverLabels),
		"spec": map[string]any{
			"replicas": 1, "strategy": map[string]any{"type": "Recreate"},
			"selector": map[string]any{"matchLabels": serverLabels},
			"template": map[string]any{"metadata": map[string]any{"labels": serverLabels}, "spec": serverPod},
		},
	}
	service := k8sObject{
		"apiVersion": "v1", "kind": "Service", "metadata": meta(name, serverLabels),
		"spec": map[string]any{"selector": serverLabels, "ports": []any{map[string]any{"name": "http", "port": serverPort, "targetPort": serverPort}}},
	}
	// Only trial pods reach the server.
	policy := k8sObject{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": meta(name, serverLabels),
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": serverLabels}, "policyTypes": []string{"Ingress"},
			"ingress": []any{map[string]any{
				"from":  []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{"k8sbench/role": "trial", "k8sbench/model": id}}}},
				"ports": []any{map[string]any{"port": serverPort}},
			}},
		},
	}
	return []k8sObject{job(name+"-fetch", fetchLabels, 6*3600, v.TTLSeconds, fetchPod), deployment, service, policy}, nil
}

func baseImageOf(v values) string {
	if v.BaseImage != "" {
		return v.BaseImage
	}
	return defaultBaseImage
}

func mount(name, at string, readOnly bool) map[string]any {
	return map[string]any{"name": name, "mountPath": at, "readOnly": readOnly}
}

func pvcVolume(name, claim string, readOnly bool) map[string]any {
	return map[string]any{"name": name, "persistentVolumeClaim": map[string]any{"claimName": claim, "readOnly": readOnly}}
}

// writeObjects prints the objects as one YAML stream.
func writeObjects(w io.Writer, objects []k8sObject) error {
	for i, o := range objects {
		if i > 0 {
			if _, err := io.WriteString(w, "---\n"); err != nil {
				return err
			}
		}
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(o); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
		if _, err := w.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// gpuFlags are the flags of the GPU workload's commands.
type gpuFlags struct {
	valuesPath *string
	apply      func(*values) error
	modelPVC   *string
	trialPVC   *string
	repo       *string
}

func addGPUFlags(fs *flag.FlagSet) gpuFlags {
	return gpuFlags{
		valuesPath: fs.String("values", "", "values file (default $K8SBENCH_VALUES, else "+defaultValuesFile+" when present)"),
		apply:      valueFlags(fs),
		modelPVC:   fs.String("model-pvc", "", "PersistentVolumeClaim for the weights (gpu.modelPVC)"),
		trialPVC:   fs.String("trial-pvc", "", "PersistentVolumeClaim for the staged bundle and tasks (gpu.trialPVC)"),
		repo:       fs.String("repo", ".", "repository root (where "+modelsLockFile+" and "+tasksLockFile+" are)"),
	}
}

func (f gpuFlags) resolve(getenv func(string) string, needModel, needTrial bool) (values, gpuValues, error) {
	v, err := loadValues(*f.valuesPath, f.apply, getenv)
	if err != nil {
		return v, gpuValues{}, err
	}
	if v.BaseImage != "" && !pinnedImage.MatchString(v.BaseImage) {
		return v, gpuValues{}, fmt.Errorf("base image %q is not pinned by digest", v.BaseImage)
	}
	g := v.GPU.withDefaults()
	if *f.modelPVC != "" {
		g.ModelPVC = *f.modelPVC
	}
	if *f.trialPVC != "" {
		g.TrialPVC = *f.trialPVC
	}
	if v.Namespace == "" || placeholder.MatchString(v.Namespace) {
		return v, g, errors.New("namespace is not set (-namespace, K8SBENCH_NAMESPACE or the values file)")
	}
	return v, g, g.validate(needModel, needTrial)
}

// cmdModel renders the model server of one locked model.
func cmdModel(args []string, stdout, stderr io.Writer, getenv func(string) string, now func() time.Time) error {
	fs := flag.NewFlagSet("model", flag.ContinueOnError)
	fs.SetOutput(stderr)
	gf := addGPUFlags(fs)
	id := fs.String("model", "", "model id in "+modelsLockFile)
	staged := fs.String("staged", "", "sha256 of the bundle the stage Job put on the trial claim (k8sbench stage prints it)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	v, g, err := gf.resolve(getenv, true, true)
	if err != nil {
		return err
	}
	l, err := readModelsLock(filepath.Join(*gf.repo, modelsLockFile))
	if err != nil {
		return err
	}
	if err := l.checkPinAges(now()); err != nil {
		return err
	}
	objects, err := renderModel(v, g, l, *id, *staged)
	if err != nil {
		return err
	}
	return writeObjects(stdout, objects)
}
