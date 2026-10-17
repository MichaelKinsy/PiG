package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
)

//go:embed templates/job.yaml
var jobTemplate string

// Session modes.
const (
	modeCompare   = "compare"
	modeCalibrate = "calibrate"
)

// plan is what the pod runs: the runner writes it into the Job as K8SBENCH_PLAN.
type plan struct {
	Run     string   `json:"run"`
	Mode    string   `json:"mode"`
	Profile *profile `json:"profile"`
	// Targets are the profile targets the session measures, in their base order; each round rotates it by one.
	Targets []string `json:"targets"`
	Turns   []int    `json:"turns"`
	Rounds  int      `json:"rounds"`
	// CPUs is how many cores every measured process is pinned to with taskset; Pin is auto (exclusive cores when
	// the pod has them, else the quietest of the allowed cores), none, or an explicit list such as 4,5.
	CPUs int    `json:"cpus"`
	Pin  string `json:"pin"`
	// Commit, when set, must match the commit the image was built from.
	Commit             string `json:"commit,omitempty"`
	StepTimeoutSeconds int    `json:"stepTimeoutSeconds"`
	// AllowFrequencyScaling runs a session whose measured cores scale their clock. CPU times then follow the clock, so
	// the analysis reports such a session but never judges it.
	AllowFrequencyScaling bool `json:"allowFrequencyScaling,omitempty"`
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

var pinList = regexp.MustCompile(`^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$`)

func (p *plan) validate() error {
	if p.Profile == nil {
		return fmt.Errorf("plan has no profile")
	}
	if err := p.Profile.validate(); err != nil {
		return err
	}
	switch p.Mode {
	case modeCompare, modeCalibrate:
	default:
		return fmt.Errorf("mode %q is not %s or %s", p.Mode, modeCompare, modeCalibrate)
	}
	if len(p.Targets) == 0 {
		return fmt.Errorf("plan has no targets")
	}
	for _, name := range p.Targets {
		if _, ok := p.Profile.byName(name); !ok {
			return fmt.Errorf("target %s is not in profile %s", name, p.Profile.Name)
		}
	}
	if len(p.Turns) == 0 {
		return fmt.Errorf("plan has no sizes")
	}
	for _, n := range p.Turns {
		if n <= 0 {
			return fmt.Errorf("size %d is not a positive turn count", n)
		}
	}
	if p.Rounds < 1 {
		return fmt.Errorf("rounds must be at least 1")
	}
	if p.CPUs < 1 {
		return fmt.Errorf("cpus must be at least 1")
	}
	if p.Pin != "auto" && p.Pin != "none" && !pinList.MatchString(p.Pin) {
		return fmt.Errorf("pin %q is not auto, none or a CPU list", p.Pin)
	}
	if p.StepTimeoutSeconds < 1 {
		return fmt.Errorf("stepTimeoutSeconds must be at least 1")
	}
	return nil
}

// sessionTargets is the base target order for a mode: calibration measures the reference against itself.
func sessionTargets(p *profile, mode string) []string {
	if mode == modeCalibrate {
		ref, _ := p.byRole(roleReference)
		return []string{ref.Name}
	}
	var names []string
	for _, t := range p.pigs() {
		names = append(names, t.Name)
	}
	for _, role := range []string{roleReference, roleFloor} {
		if t, ok := p.byRole(role); ok {
			names = append(names, t.Name)
		}
	}
	return names
}

// runID names one session: mode, image commit (or "image" when the runner does not know it) and UTC start time.
// It is also the Job name, so it stays a DNS label of at most 63 characters.
func runID(mode, commit string, now time.Time) string {
	short := "cal"
	if mode == modeCompare {
		short = "cmp"
	}
	c := strings.ToLower(commit)
	if len(c) > 10 {
		c = c[:10]
	}
	if c == "" || !dnsLabel.MatchString(c) {
		c = "image"
	}
	return fmt.Sprintf("k8sbench-%s-%s-%s", short, c, now.UTC().Format("20060102t150405"))
}

type envVar struct{ Name, Value string }

type jobData struct {
	Name, Namespace, Image, CPU, Memory, ScratchSize, Node, PVC, ServiceAccountName string
	Command                                                                         []string
	Env                                                                             []envVar
	Bundle                                                                          bool
	Labels, NodeSelector                                                            map[string]string
	Tolerations                                                                     []toleration
	ImagePullSecrets                                                                []map[string]string
	Plan                                                                            string
	DeadlineSeconds, TTLSeconds                                                     int
}

func jobLabels(pl *plan) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "k8sbench",
		"app.kubernetes.io/managed-by": "k8sbench",
		"k8sbench/mode":                pl.Mode,
		"k8sbench/profile":             pl.Profile.Name,
	}
}

// bundleRef is what the Job carries about a bundle: the packed file's sha256, which the pod checks the copy against,
// and its architecture, which the pod checks the node against.
type bundleRef struct{ sha256, arch string }

// renderJob renders the Job that runs one session. Every cluster-specific value comes from v; b describes the bundle
// in bundle mode.
func renderJob(v values, pl *plan, b bundleRef) ([]byte, error) {
	if err := pl.validate(); err != nil {
		return nil, err
	}
	if pl.CPUs != v.CPUs {
		return nil, fmt.Errorf("plan pins %d cores but the pod requests %d CPUs", pl.CPUs, v.CPUs)
	}
	encoded, err := marshalPlain(pl)
	if err != nil {
		return nil, err
	}
	data := jobData{
		Name:               pl.Run,
		Namespace:          v.Namespace,
		Image:              v.podImage(),
		CPU:                strconv.Itoa(v.CPUs),
		Memory:             v.Memory,
		ScratchSize:        v.ScratchSize,
		Node:               v.Node,
		PVC:                v.PVC,
		ServiceAccountName: v.ServiceAccountName,
		Labels:             jobLabels(pl),
		NodeSelector:       v.NodeSelector,
		Tolerations:        v.Tolerations,
		Plan:               string(encoded),
		DeadlineSeconds:    v.DeadlineSeconds,
		TTLSeconds:         v.TTLSeconds,
	}
	data.Env = []envVar{{"K8SBENCH_DELIVERY", v.delivery()}, {"K8SBENCH_IMAGE_REF", v.podImage()}}
	if v.delivery() == deliveryBundle {
		if !sha256Hex.MatchString(b.sha256) || (b.arch != "amd64" && b.arch != "arm64") {
			return nil, fmt.Errorf("bundle mode needs the bundle's sha256 and architecture, got %q and %q", b.sha256, b.arch)
		}
		data.Bundle = true
		data.Command = []string{"sh", "-c", podBundleScript}
		data.Env = append(data.Env,
			envVar{"K8SBENCH_BUNDLE_SHA256", b.sha256},
			envVar{"K8SBENCH_BUNDLE_ARCH", b.arch},
			envVar{"K8SBENCH_ROOT", bundleDir + "/pig"},
			envVar{"PATH", bundleDir + "/pig/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		)
	} else {
		data.Command = []string{"/opt/pig/bin/k8sbench", "pod"}
	}
	for _, s := range slices.Sorted(slices.Values(v.ImagePullSecrets)) {
		data.ImagePullSecrets = append(data.ImagePullSecrets, map[string]string{"name": s})
	}
	tmpl, err := template.New("job.yaml").Option("missingkey=error").Funcs(template.FuncMap{
		"json": func(value any) (string, error) {
			b, err := marshalPlain(value)
			return string(b), err
		},
	}).Parse(jobTemplate)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
