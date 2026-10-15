package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

//go:embed profiles/*.json
var builtinProfiles embed.FS

// Target roles. Each tracker line compares one pig target with the reference target and nets both of the floor
// target's p50 when the profile has one; a profile with several pig targets writes one line per pig target and
// size. Calibration runs the reference target alone.
const (
	rolePig       = "pig"
	roleReference = "reference"
	roleFloor     = "floor"
)

// profile is a workload: the targets one session measures and how the image runs them. Commands are argv lists
// whose {root}, {dir}, {turns} and {sample} placeholders the pod fills in: {root} is the image's install root,
// {dir} the target's own fixture directory for one size, {sample} the round index.
type profile struct {
	Name  string `json:"name"`
	Rule  string `json:"rule"`
	About string `json:"about"`
	// Fingerprints are the history fingerprints every target's seed must reproduce, by size.
	Fingerprints map[string]string `json:"fingerprints"`
	Targets      []target          `json:"targets"`
	// Metrics are per-sample fields the workload reports beyond durable-bench's, such as the resume or redraw time of
	// a long session. Each is a lower-is-better value of one fresh process.
	//portlint:allow emptydrop benchmark tool output, not Pi-facing
	Metrics []metric `json:"metrics,omitempty"`
	// BaseImage is the image a bundle session of the profile runs on, pinned by digest, when its programs need another
	// than the default (such as a newer glibc). It runs from a bundle only.
	BaseImage string `json:"baseImage,omitempty"`
}

// metric is a workload-specific sample field. The analysis reports its CV over the reference's samples, and the
// tracker carries the median of the pig and reference samples as <name> and pi<Name> with a verdict, when every
// sample of both has it. A gated metric is part of the noise gate and must be in every reference sample.
type metric struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Gate  bool   `json:"gate,omitempty"`
}

var (
	metricPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,40}$`)
	refValue      = regexp.MustCompile(`^pi[A-Z]`)
)

// reservedMetrics are the sample fields the pod or the analysis already gives a meaning and the tracker values. The pod
// writes the image commit as a pig target's version.
var reservedMetrics = []string{"t", "target", "version", "turns", "sample", "open", "turn", "fingerprint", "openCpu", "turnCpu",
	"warm", "warmP95", "warmMax", "cold", "coldP95", "coldMax", "cpu", "cpuWarm", "cpuCold", "cpuSeconds", "peakRss",
	"peakRssMB", "retained", "retainedMB", "growthPerTurn", "bytes", "seedS", "floorWarm", "floorCold", "gomaxprocs", "cpuModel", "cpuFeatures"}

// refName is the tracker value of the reference target's median of a metric.
func (m metric) refName() string { return "pi" + strings.ToUpper(m.Name[:1]) + m.Name[1:] }

// gated are the names of the profile's gated metrics.
func (p *profile) gated() []string {
	var out []string
	for _, m := range p.Metrics {
		if m.Gate {
			out = append(out, m.Name)
		}
	}
	return out
}

type target struct {
	Name string `json:"name"`
	Role string `json:"role"`
	// Seed builds the target's fixture from zero; SeedMeta is the JSON file it writes (seedMs, fingerprint, bytes).
	// A target without Seed, such as a floor, measures without a fixture.
	Seed     []string `json:"seed"`
	SeedMeta string   `json:"seedMeta"`
	// Sample runs one fresh process that prints one durable-bench result line (open, turn[]) on stdout.
	Sample []string `json:"sample"`
	// Rule is a pig target's tracker rule. It defaults to the profile's rule when the profile has one pig target and
	// to "<profile rule>-<target name>" when it has several, so their rolling histories never mix.
	Rule string `json:"rule,omitempty"`
}

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

func (p *profile) validate() error {
	if !namePattern.MatchString(p.Name) {
		return fmt.Errorf("profile name %q: use lower-case letters, digits and dashes", p.Name)
	}
	if p.Rule == "" {
		return fmt.Errorf("profile %s: rule is required (it keys the tracker's rolling history)", p.Name)
	}
	roles := map[string]int{}
	seen := map[string]bool{}
	for _, t := range p.Targets {
		if !namePattern.MatchString(t.Name) {
			return fmt.Errorf("profile %s: target name %q: use lower-case letters, digits and dashes", p.Name, t.Name)
		}
		if seen[t.Name] {
			return fmt.Errorf("profile %s: target %s appears twice", p.Name, t.Name)
		}
		seen[t.Name] = true
		switch t.Role {
		case rolePig, roleReference, roleFloor:
		default:
			return fmt.Errorf("profile %s: target %s: role %q is not pig, reference or floor", p.Name, t.Name, t.Role)
		}
		roles[t.Role]++
		if len(t.Sample) == 0 {
			return fmt.Errorf("profile %s: target %s has no sample command", p.Name, t.Name)
		}
		if t.Role != roleFloor && len(t.Seed) == 0 {
			return fmt.Errorf("profile %s: target %s: only a floor target may run without a seed command", p.Name, t.Name)
		}
		if len(t.Seed) > 0 && t.SeedMeta == "" {
			return fmt.Errorf("profile %s: target %s has a seed command but no seedMeta file", p.Name, t.Name)
		}
	}
	if roles[rolePig] < 1 || roles[roleReference] != 1 || roles[roleFloor] > 1 {
		return fmt.Errorf("profile %s: needs at least one pig target, exactly one reference target and at most one floor", p.Name)
	}
	rules := map[string]string{}
	for _, t := range p.Targets {
		if t.Rule != "" && t.Role != rolePig {
			return fmt.Errorf("profile %s: target %s: only a pig target has its own rule", p.Name, t.Name)
		}
		if t.Role != rolePig {
			continue
		}
		r := p.ruleOf(t)
		if other, ok := rules[r]; ok {
			return fmt.Errorf("profile %s: pig targets %s and %s share the rule %s; their tracker histories would mix", p.Name, other, t.Name, r)
		}
		rules[r] = t.Name
	}
	names := map[string]bool{}
	for _, m := range p.Metrics {
		if !metricPattern.MatchString(m.Name) {
			return fmt.Errorf("profile %s: metric name %q: use a camelCase sample field name", p.Name, m.Name)
		}
		// pi<Name> is a reference value in the tracker line.
		if refValue.MatchString(m.Name) || slices.Contains(reservedMetrics, m.Name) || names[m.Name] {
			return fmt.Errorf("profile %s: metric %s is a field the analysis already reads or appears twice", p.Name, m.Name)
		}
		if strings.TrimSpace(m.Label) == "" {
			return fmt.Errorf("profile %s: metric %s has no label for the tracker summary", p.Name, m.Name)
		}
		names[m.Name] = true
	}
	if p.BaseImage != "" && !imageDigest.MatchString(p.BaseImage) {
		return fmt.Errorf("profile %s: baseImage %q is not pinned by digest (name@sha256:...)", p.Name, p.BaseImage)
	}
	for size := range p.Fingerprints {
		if n, err := strconv.Atoi(size); err != nil || n <= 0 {
			return fmt.Errorf("profile %s: fingerprint size %q is not a positive integer", p.Name, size)
		}
	}
	return nil
}

// pigs are the profile's pig targets in profile order.
func (p *profile) pigs() []target {
	var out []target
	for _, t := range p.Targets {
		if t.Role == rolePig {
			out = append(out, t)
		}
	}
	return out
}

// ruleOf is the tracker rule of a pig target's lines.
func (p *profile) ruleOf(t target) string {
	switch {
	case t.Rule != "":
		return t.Rule
	case len(p.pigs()) == 1:
		return p.Rule
	}
	return p.Rule + "-" + t.Name
}

func (p *profile) byRole(role string) (target, bool) {
	i := slices.IndexFunc(p.Targets, func(t target) bool { return t.Role == role })
	if i < 0 {
		return target{}, false
	}
	return p.Targets[i], true
}

func (p *profile) byName(name string) (target, bool) {
	i := slices.IndexFunc(p.Targets, func(t target) bool { return t.Name == name })
	if i < 0 {
		return target{}, false
	}
	return p.Targets[i], true
}

// loadProfile reads a built-in profile by name or a profile file by path.
func loadProfile(nameOrPath string) (*profile, error) {
	var data []byte
	var err error
	if strings.ContainsAny(nameOrPath, `/\`) || strings.HasSuffix(nameOrPath, ".json") {
		data, err = os.ReadFile(nameOrPath)
	} else {
		data, err = builtinProfiles.ReadFile("profiles/" + nameOrPath + ".json")
		if err != nil {
			return nil, fmt.Errorf("no built-in profile %q (built in: %s); pass a file path for your own", nameOrPath, strings.Join(builtinProfileNames(), ", "))
		}
	}
	if err != nil {
		return nil, err
	}
	var p profile
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return nil, fmt.Errorf("profile %s: %w", nameOrPath, err)
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func builtinProfileNames() []string {
	entries, _ := builtinProfiles.ReadDir("profiles")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".json"))
	}
	return names
}

// expand fills a command's placeholders.
func expand(args []string, vars map[string]string) []string {
	pairs := make([]string, 0, 2*len(vars))
	for _, k := range slices.Sorted(maps.Keys(vars)) {
		pairs = append(pairs, "{"+k+"}", vars[k])
	}
	replacer := strings.NewReplacer(pairs...)
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = replacer.Replace(a)
	}
	return out
}

// applyBaseImage makes a profile's base image the session's: the profile runs from a bundle on it, and a base image
// set otherwise must be the same one.
func applyBaseImage(p *profile, v *values) error {
	switch {
	case p.BaseImage == "":
		return nil
	case v.delivery() != deliveryBundle:
		return fmt.Errorf("profile %s runs from a bundle (-bundle) on its base image %s, which its programs need", p.Name, p.BaseImage)
	case v.BaseImage == "":
		v.BaseImage = p.BaseImage
		return nil
	case v.BaseImage != p.BaseImage:
		return fmt.Errorf("profile %s runs on its base image %s, which its programs need, not %s", p.Name, p.BaseImage, v.BaseImage)
	}
	return nil
}
