package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// values is the cluster-specific configuration. It comes from a values file (bench/k8s/values.example.yaml shows
// every field; keep the real one in the gitignored bench/k8s/values.local.yaml), then the environment, then flags.
// Nothing cluster-specific has a default.
type values struct {
	Kubeconfig  string `yaml:"kubeconfig"`
	KubeContext string `yaml:"kubeContext"`
	Namespace   string `yaml:"namespace"`
	Image       string `yaml:"image"`
	// AllowUnpinnedImage accepts an image reference without a @sha256: digest. Results then do not identify the
	// image they ran on.
	AllowUnpinnedImage bool `yaml:"allowUnpinnedImage"`
	// Bundle, instead of Image, names a local bundle directory (k8sbench bundle, or bench/k8s/image/build.sh with
	// BUNDLE=DIR). The pod then runs BaseImage and the runner copies the bundle in, so no registry is needed.
	Bundle       string            `yaml:"bundle"`
	BaseImage    string            `yaml:"baseImage"`
	CPUs         int               `yaml:"cpus"`
	Memory       string            `yaml:"memory"`
	ScratchSize  string            `yaml:"scratchSize"`
	NodeSelector map[string]string `yaml:"nodeSelector"`
	Tolerations  []toleration      `yaml:"tolerations"`
	// Node pins the pod to one node by name (a required node affinity on metadata.name, so the scheduler still
	// checks taints and resources).
	Node               string   `yaml:"node"`
	PVC                string   `yaml:"pvc"`
	ImagePullSecrets   []string `yaml:"imagePullSecrets"`
	ServiceAccountName string   `yaml:"serviceAccountName"`
	DeadlineSeconds    int      `yaml:"deadlineSeconds"`
	TTLSeconds         int      `yaml:"ttlSecondsAfterFinished"`
}

type toleration struct {
	Key               string `yaml:"key" json:"key,omitempty"`
	Operator          string `yaml:"operator" json:"operator,omitempty"`
	Value             string `yaml:"value" json:"value,omitempty"`
	Effect            string `yaml:"effect" json:"effect,omitempty"`
	TolerationSeconds *int64 `yaml:"tolerationSeconds" json:"tolerationSeconds,omitempty"`
}

// defaultValuesFile is read when present and no other file is named; it is gitignored.
const defaultValuesFile = "bench/k8s/values.local.yaml"

func defaultValues() values {
	return values{CPUs: 2, Memory: "8Gi", ScratchSize: "8Gi", DeadlineSeconds: 6 * 3600, TTLSeconds: 7 * 24 * 3600}
}

func loadValuesFile(path string, v *values) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	// An empty file decodes as io.EOF and leaves the defaults.
	if err := decoder.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// applyEnv overrides values from the environment. KUBECONFIG is read by kubectl itself; it is recorded here only so
// a values file can name a kubeconfig the environment does not.
func applyEnv(v *values, getenv func(string) string) error {
	if s := getenv("K8SBENCH_NAMESPACE"); s != "" {
		v.Namespace = s
	}
	if s := getenv("K8SBENCH_IMAGE"); s != "" {
		v.Image = s
	}
	if s := getenv("K8SBENCH_BUNDLE"); s != "" {
		v.Bundle = s
	}
	if s := getenv("K8SBENCH_BASE_IMAGE"); s != "" {
		v.BaseImage = s
	}
	if s := getenv("K8SBENCH_CONTEXT"); s != "" {
		v.KubeContext = s
	}
	if s := getenv("K8SBENCH_NODE"); s != "" {
		v.Node = s
	}
	if s := getenv("K8SBENCH_PVC"); s != "" {
		v.PVC = s
	}
	if s := getenv("K8SBENCH_CPUS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("K8SBENCH_CPUS=%q: %w", s, err)
		}
		v.CPUs = n
	}
	if s := getenv("K8SBENCH_MEMORY"); s != "" {
		v.Memory = s
	}
	return nil
}

var (
	dnsLabel     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)
	dnsSubdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)
	quantity     = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(Ki|Mi|Gi|Ti|k|M|G|T)?$`)
	labelKey     = regexp.MustCompile(`^([a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`)
	labelValue   = regexp.MustCompile(`^([A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?)?$`)
	imageDigest  = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	placeholder  = regexp.MustCompile(`<[^>]*>`)
)

func (v *values) validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	for name, s := range map[string]string{"namespace": v.Namespace, "image": v.Image, "bundle": v.Bundle, "baseImage": v.BaseImage, "node": v.Node, "pvc": v.PVC, "kubeContext": v.KubeContext} {
		if placeholder.MatchString(s) {
			add("%s %q still holds a placeholder from values.example.yaml", name, s)
		}
	}
	if v.Namespace == "" {
		add("namespace is not set (-namespace, K8SBENCH_NAMESPACE or the values file)")
	} else if !dnsLabel.MatchString(v.Namespace) {
		add("namespace %q is not a DNS label", v.Namespace)
	}
	switch {
	case v.Image != "" && v.Bundle != "":
		add("both image (%s) and bundle (%s) are set; a session runs one of them", v.Image, v.Bundle)
	case v.Image == "" && v.Bundle == "":
		add("neither image (-image, K8SBENCH_IMAGE) nor bundle (-bundle, K8SBENCH_BUNDLE) is set")
	case v.Image != "" && !v.AllowUnpinnedImage && !imageDigest.MatchString(v.Image):
		add("image %q is not pinned by digest (name@sha256:...); bench/k8s/image/build.sh prints the pinned reference", v.Image)
	case v.Bundle != "" && !v.AllowUnpinnedImage && !imageDigest.MatchString(v.podImage()):
		add("base image %q is not pinned by digest (name@sha256:...)", v.podImage())
	}
	if v.CPUs < 1 {
		add("cpus must be a whole number of at least 1 (Guaranteed QoS needs an integer CPU request for exclusive cores)")
	}
	if !quantity.MatchString(v.Memory) {
		add("memory %q is not a Kubernetes quantity", v.Memory)
	}
	if !quantity.MatchString(v.ScratchSize) {
		add("scratchSize %q is not a Kubernetes quantity", v.ScratchSize)
	}
	if v.Node != "" && !dnsSubdomain.MatchString(v.Node) {
		add("node %q is not a node name", v.Node)
	}
	if v.PVC != "" && !dnsSubdomain.MatchString(v.PVC) {
		add("pvc %q is not a claim name", v.PVC)
	}
	if v.ServiceAccountName != "" && !dnsSubdomain.MatchString(v.ServiceAccountName) {
		add("serviceAccountName %q is not a valid name", v.ServiceAccountName)
	}
	for _, s := range v.ImagePullSecrets {
		if !dnsSubdomain.MatchString(s) {
			add("imagePullSecrets entry %q is not a secret name", s)
		}
	}
	for k, val := range v.NodeSelector {
		if !labelKey.MatchString(k) || !labelValue.MatchString(val) {
			add("nodeSelector %q: %q is not a label selector", k, val)
		}
	}
	for _, t := range v.Tolerations {
		switch t.Operator {
		case "", "Equal", "Exists":
		default:
			add("toleration operator %q is not Equal or Exists", t.Operator)
		}
		switch t.Effect {
		case "", "NoSchedule", "PreferNoSchedule", "NoExecute":
		default:
			add("toleration effect %q is not NoSchedule, PreferNoSchedule or NoExecute", t.Effect)
		}
	}
	if v.DeadlineSeconds < 60 {
		add("deadlineSeconds must be at least 60")
	}
	if v.TTLSeconds < 0 {
		add("ttlSeconds must not be negative")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n  "))
	}
	return nil
}

// delivery is how the pod gets its artifacts.
func (v *values) delivery() string {
	if v.Bundle != "" {
		return deliveryBundle
	}
	return deliveryImage
}

// podImage is the image the pod runs: the benchmark image, or in bundle mode the base image.
func (v *values) podImage() string {
	if v.Bundle == "" {
		return v.Image
	}
	if v.BaseImage != "" {
		return v.BaseImage
	}
	return defaultBaseImage
}

// valueFlags registers the flags that override values and returns a function that applies the flags the user set.
func valueFlags(fs *flag.FlagSet) func(*values) error {
	kubeconfig := fs.String("kubeconfig", "", "kubeconfig file (default: kubectl's own, $KUBECONFIG)")
	kubeContext := fs.String("context", "", "kubectl context ($K8SBENCH_CONTEXT)")
	namespace := fs.String("namespace", "", "namespace for the Job ($K8SBENCH_NAMESPACE)")
	image := fs.String("image", "", "benchmark image, pinned by digest ($K8SBENCH_IMAGE)")
	allowUnpinned := fs.Bool("allow-unpinned-image", false, "accept an image without a @sha256: digest")
	bundle := fs.String("bundle", "", "bundle directory to copy into a base-image pod instead of using -image ($K8SBENCH_BUNDLE)")
	baseImage := fs.String("base-image", "", "base image for -bundle, pinned by digest ($K8SBENCH_BASE_IMAGE; default "+defaultBaseImage+")")
	cpus := fs.Int("cpus", 0, "whole CPUs the pod requests and is limited to, and the cores the session pins to (default 2)")
	memory := fs.String("memory", "", "memory request and limit (default 8Gi)")
	node := fs.String("node", "", "pin the pod to this node by name ($K8SBENCH_NODE)")
	selector := fs.String("node-selector", "", "node selector, key=value[,key=value] (replaces the values file's)")
	pvc := fs.String("pvc", "", "PersistentVolumeClaim mounted at /results for a copy of the raw output ($K8SBENCH_PVC)")
	deadline := fs.Int("deadline", 0, "Job activeDeadlineSeconds (default 21600)")
	return func(v *values) error {
		var err error
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "kubeconfig":
				v.Kubeconfig = *kubeconfig
			case "context":
				v.KubeContext = *kubeContext
			case "namespace":
				v.Namespace = *namespace
			case "image":
				v.Image = *image
			case "allow-unpinned-image":
				v.AllowUnpinnedImage = *allowUnpinned
			case "bundle":
				v.Bundle = *bundle
			case "base-image":
				v.BaseImage = *baseImage
			case "cpus":
				v.CPUs = *cpus
			case "memory":
				v.Memory = *memory
			case "node":
				v.Node = *node
			case "node-selector":
				v.NodeSelector, err = parseSelector(*selector)
			case "pvc":
				v.PVC = *pvc
			case "deadline":
				v.DeadlineSeconds = *deadline
			}
		})
		return err
	}
}

func parseSelector(s string) (map[string]string, error) {
	out := map[string]string{}
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		k, val, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("node selector %q: want key=value", part)
		}
		out[k] = val
	}
	return out, nil
}

// resolveValues layers defaults, the values file, the environment and the flags the user set.
func resolveValues(valuesPath string, applyFlags func(*values) error, getenv func(string) string) (values, error) {
	v := defaultValues()
	path := valuesPath
	if path == "" {
		path = getenv("K8SBENCH_VALUES")
	}
	if path == "" {
		if _, err := os.Stat(defaultValuesFile); err == nil {
			path = defaultValuesFile
		}
	}
	if path != "" {
		if err := loadValuesFile(path, &v); err != nil {
			return v, err
		}
	}
	if err := applyEnv(&v, getenv); err != nil {
		return v, err
	}
	if err := applyFlags(&v); err != nil {
		return v, err
	}
	return v, v.validate()
}
