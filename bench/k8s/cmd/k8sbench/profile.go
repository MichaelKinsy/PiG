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
