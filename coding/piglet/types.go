// Piglet YAML schema types and parser.
//
// A piglet is the complete agent definition: extensions with per-source
// capability scoping, MCP servers, skills, prompts, model config. The same
// YAML format is used locally (~/.pig/piglets/), on platform (ConfigMap
// from Agent CRD spec.piglet), and in independently published Piglet source.
//
// Piglet is the agent-author scoping mechanism. A hosting platform may apply
// an additional administrative policy ceiling without rewriting the Piglet.
package piglet

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	agenttools "github.com/MichaelKinsy/PiG/internal/codingagent/tools"

	"gopkg.in/yaml.v3"
)

// checkKnownKeys validates that all mapping keys in a YAML node are recognized
// struct tags for the target type. Returns an error for the first unknown key.
// This enforces strict schema validation inside custom UnmarshalYAML methods
// where the top-level Decoder.KnownFields(true) doesn't reach.
func checkKnownKeys(node *yaml.Node, target any) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	known := knownYAMLKeys(reflect.TypeOf(target))
	for i := 0; i < len(node.Content)-1; i += 2 {
		key := node.Content[i].Value
		if !known[key] {
			return fmt.Errorf("line %d: unknown field %q", node.Content[i].Line, key)
		}
	}
	return nil
}

func knownYAMLKeys(t reflect.Type) map[string]bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	out := map[string]bool{}
	for field := range t.Fields() {
		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		out[name] = true
	}
	return out
}

// SkillEntry represents a skill with an origin.
type SkillEntry struct {
	Name        string   `yaml:"name"`
	Origins     []string `yaml:"origins,omitempty"` // Required unless Content is embedded.
	Description string   `yaml:"description,omitempty"`
	Content     string   `yaml:"content,omitempty"`
}

// UnmarshalYAML handles the bare skill-name shorthand.
func (s *SkillEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		s.Name = node.Value
		return nil
	}
	if err := checkKnownKeys(node, SkillEntry{}); err != nil {
		return err
	}
	type raw SkillEntry
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*s = SkillEntry(r)
	return nil
}

// Piglet is the top-level piglet YAML structure.
type Piglet struct {
	Name             string              `yaml:"name"`
	Description      string              `yaml:"description,omitempty"`
	Extends          *ExtendsSpec        `yaml:"extends,omitempty"`
	BuiltinTools     *[]string           `yaml:"tools,omitempty"`
	Packages         map[string]string   `yaml:"packages,omitempty"`
	Extensions       []ExtensionEntry    `yaml:"extensions,omitempty"`
	Skills           []SkillEntry        `yaml:"skills,omitempty"`
	Discovery        *Discovery          `yaml:"discovery,omitempty"`
	SystemPrompt     *PromptRef          `yaml:"systemPrompt,omitempty"`
	AgentEnv         *AgentEnvironment   `yaml:"agentEnv,omitempty"`
	Model            *ModelConfig        `yaml:"model,omitempty"`
	Build            *BuildSpec          `yaml:"build,omitempty"`
	Release          *ReleaseSpec        `yaml:"release,omitempty"`
	Secrets          []SecretDeclaration `yaml:"secrets,omitempty"`
	sourceDir        string              // Parse(path) anchor for relative origins; never serialized.
	sourcePath       string              // Canonical parsed Piglet path; never serialized.
	packageMu        sync.Mutex
	packageResults   map[string]packageResolution
	present          map[string]bool // authored top-level fields; never serialized
	nullFields       map[string]bool // authored YAML null fields; never serialized
	lineage          []LineageEntry  // exact resolved lineage; never serialized
	effectiveDigest  string
	graphDigest      string
	workspaceRoot    string // invocation-selected workspace; never serialized
	devContainerPath string // resolved workspace-anchored path; never serialized
}

// ExtendsSpec selects one base Piglet source plus an optional author version
// constraint. Resolution pins the exact content digest in lineage.
type ExtendsSpec struct {
	Source     string      `yaml:"source"`
	Version    string      `yaml:"version,omitempty"`
	AllowWiden bool        `yaml:"allowWiden,omitempty"`
	Remove     *RemoveSpec `yaml:"remove,omitempty"`
}

// RemoveSpec names inherited collection members removed before child
// replacement/addition.
type RemoveSpec struct {
	Packages   []string `yaml:"packages,omitempty"`
	Extensions []string `yaml:"extensions,omitempty"`
	Skills     []string `yaml:"skills,omitempty"`
}

// SecretDeclaration declares a logical secret name and exactly one machine-
// local source. Values never enter portable Piglet state.
type SecretDeclaration struct {
	Name string       `yaml:"name"`
	From SecretSource `yaml:"from"`
}

// SecretSource is the closed env|file|ref source union.
type SecretSource struct {
	Env  string `yaml:"env,omitempty"`
	File string `yaml:"file,omitempty"`
	Ref  string `yaml:"ref,omitempty"`
}

// AgentSecretBinding maps one logical secret to a child environment target.
type AgentSecretBinding struct {
	SecretRef string            `yaml:"secretRef"`
	Target    AgentSecretTarget `yaml:"target"`
}

// AgentSecretTarget currently supports child environment injection. File
// targets wait for the environment runtime materialization slice.
type AgentSecretTarget struct {
	Env string `yaml:"env,omitempty"`
}

// ValidateAgentEnvironmentRuntime blocks a Piglet Binary build until the
// builder can preserve and execute the required whole-process environment. Launch support is checked
// separately because runtime slices can land before portable Binary/Image execution.
func (p *Piglet) ValidateAgentEnvironmentRuntime() error {
	if p == nil || p.AgentEnv == nil {
		return nil
	}
	return fmt.Errorf("Piglet %q requires agentEnv, but Piglet Binary/Image whole-process environment execution is not implemented; host fallback is forbidden", p.Name)
}

// ValidateAgentEnvironmentLaunch reports whether the current runtime slice can
// launch this Piglet without inspecting machine-local engine availability.
func (p *Piglet) ValidateAgentEnvironmentLaunch() error {
	if p == nil || p.AgentEnv == nil {
		return nil
	}
	effective := p.EffectiveAgentEnvironment()
	if p.AgentEnv.Image == "" {
		form := "Dev Container"
		if p.AgentEnv.Source != "" {
			form = "materialized source"
		}
		return fmt.Errorf("Piglet %q agentEnv requires %s execution, which is not implemented; host fallback is forbidden", p.Name, form)
	}
	if effective.PigRuntime.Mode != "image" {
		return fmt.Errorf("Piglet %q agentEnv runtime injection is not implemented; host fallback is forbidden", p.Name)
	}
	switch effective.Policy.Preset {
	case "minimal", "standard", "elevated":
	default:
		return fmt.Errorf("Piglet %q agentEnv policy preset %q is not implemented; host fallback is forbidden", p.Name, effective.Policy.Preset)
	}
	return validateImageRuntimePigletClosure(p)
}

// ExtensionEntry selects one extension and optionally limits its model tools.
// A nil Tools pointer means every registered tool; a non-nil empty slice means
// none.
type ExtensionEntry struct {
	Name    string    `yaml:"name"`
	Origins []string  `yaml:"origins,omitempty"`
	Tools   *[]string `yaml:"tools,omitempty"`
}

// UnmarshalYAML handles the bare extension-name shorthand.
func (e *ExtensionEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		e.Name = node.Value
		return nil
	}
	if err := checkKnownKeys(node, ExtensionEntry{}); err != nil {
		return err
	}
	type raw ExtensionEntry
	var value raw
	if err := node.Decode(&value); err != nil {
		return err
	}
	*e = ExtensionEntry(value)
	return nil
}

// Discovery selects ambient Resource scopes by kind. Explicit Piglet entries
// are independent of discovery. Nil and empty lists both mean no ambient
// Resources in an active Piglet.
type Discovery struct {
	Extensions []string `yaml:"extensions,omitempty"`
	Skills     []string `yaml:"skills,omitempty"`
}

// AgentEnvironment is a required whole-agent runtime when present. Exactly one
// source form is set; runtime and policy defaults are projected without
// rewriting the authored Piglet.
type AgentEnvironment struct {
	Image        string                  `yaml:"image,omitempty"`
	DevContainer string                  `yaml:"devContainer,omitempty"`
	Source       string                  `yaml:"source,omitempty"`
	PigRuntime   *AgentPigRuntime        `yaml:"pigRuntime,omitempty"`
	Policy       *AgentEnvironmentPolicy `yaml:"policy,omitempty"`
	Workspace    *AgentWorkspace         `yaml:"workspace,omitempty"`
	Mounts       []AgentMount            `yaml:"mounts,omitempty"`
	Secrets      []AgentSecretBinding    `yaml:"secrets,omitempty"`
}

// AgentWorkspace selects where the invocation workspace lands inside the
// environment. The host source stays a machine-local execution input (default
// the current directory), so it is intentionally not a Piglet field.
type AgentWorkspace struct {
	Folder   string `yaml:"folder,omitempty"`   // container path; default image WorkingDir then /workspace
	ReadOnly *bool  `yaml:"readonly,omitempty"` // nil = policy default (minimal read-only, else read-write)
}

// AgentMount is one additional host bind exposed to the environment. Extra
// mounts are an elevated-policy capability because each one widens the host
// surface the agent can read or write.
type AgentMount struct {
	Source   string `yaml:"source"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"readonly,omitempty"`
}

func (m *AgentMount) UnmarshalYAML(node *yaml.Node) error {
	if err := checkKnownKeys(node, AgentMount{}); err != nil {
		return err
	}
	type raw AgentMount
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*m = AgentMount(r)
	return nil
}

func (w *AgentWorkspace) UnmarshalYAML(node *yaml.Node) error {
	if err := checkKnownKeys(node, AgentWorkspace{}); err != nil {
		return err
	}
	type raw AgentWorkspace
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*w = AgentWorkspace(r)
	return nil
}

type AgentPigRuntime struct {
	Mode    string `yaml:"mode,omitempty"`
	Version string `yaml:"version,omitempty"`
}

type AgentEnvironmentPolicy struct {
	Preset string `yaml:"preset,omitempty"`
}

// EffectiveAgentEnvironment applies semantic defaults without mutating source.
func (p *Piglet) EffectiveAgentEnvironment() *AgentEnvironment {
	if p == nil || p.AgentEnv == nil {
		return nil
	}
	effective := *p.AgentEnv
	if effective.PigRuntime == nil {
		effective.PigRuntime = &AgentPigRuntime{Mode: "inject", Version: "latest"}
	} else {
		runtime := *effective.PigRuntime
		if runtime.Mode == "" {
			runtime.Mode = "inject"
		}
		if runtime.Version == "" {
			runtime.Version = "latest"
		}
		effective.PigRuntime = &runtime
	}
	if effective.Policy == nil {
		effective.Policy = &AgentEnvironmentPolicy{Preset: "standard"}
	} else {
		policy := *effective.Policy
		if policy.Preset == "" {
			policy.Preset = "standard"
		}
		effective.Policy = &policy
	}
	return &effective
}

// PromptRef is either a file path or inline text.
type PromptRef struct {
	File string `yaml:"file,omitempty"`
	Text string `yaml:"text,omitempty"`
}

// BuildSpec defines portable build defaults. Builder selection and verification
// policy remain execution inputs and are intentionally not serialized here.
// pig additive (D18): Piglets own Pig's additive artifact build defaults.
// BuildSpec carries portable build defaults only. Delivery tier, strictness,
// update endpoint, release version, builder, credentials, and verification are
// execution or publication inputs, not portable Piglet source.
type BuildSpec struct {
	Targets              []string `yaml:"targets,omitempty"`
	OutputName           string   `yaml:"outputName,omitempty"`
	ExtensionRealization string   `yaml:"extensionRealization,omitempty"`
}

// ReleaseSpec carries Piglet release identity, separate from portable build
// defaults. release.version is the distributed artifact's SemVer.
type ReleaseSpec struct {
	Version string `yaml:"version,omitempty"`
}

// ModelConfig specifies the model to use.
type ModelConfig struct {
	Provider      string `yaml:"provider,omitempty"`
	Name          string `yaml:"name,omitempty"`
	ContextWindow int    `yaml:"contextWindow,omitempty"`
	Thinking      string `yaml:"thinking,omitempty"`
}

// Parse reads and validates a piglet YAML file.
func Parse(path string) (*Piglet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read piglet %s: %w", path, err)
	}
	piglet, err := ParseBytes(data)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve piglet path %s: %w", path, err)
	}
	piglet.sourcePath = canonicalPath(absolute)
	piglet.sourceDir = canonicalPath(filepath.Dir(absolute))
	return piglet, nil
}

// ParseBytes parses and validates piglet YAML from bytes.
// Unknown keys are rejected to catch typos and unsupported fields early.
func ParseBytes(data []byte) (*Piglet, error) {
	var p Piglet
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("parse piglet YAML: %w", err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse piglet YAML: %w", err)
	}
	if len(document.Content) > 0 && document.Content[0].Kind == yaml.MappingNode {
		root := document.Content[0]
		p.present = make(map[string]bool, len(root.Content)/2)
		p.nullFields = make(map[string]bool)
		for i := 0; i < len(root.Content)-1; i += 2 {
			field := root.Content[i].Value
			p.present[field] = true
			if root.Content[i+1].Tag == "!!null" {
				p.nullFields[field] = true
			}
		}
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

var (
	pigletNamePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	pigletTargetPartPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	pigletWindowsPathPattern = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
	environmentNamePattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	secretResolverRefPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*:[^[:space:]]+$`)
)

// Validate checks the piglet for schema violations.
func (p *Piglet) Validate() error {
	if len(p.nullFields) > 0 {
		fields := slices.Sorted(maps.Keys(p.nullFields))
		return fmt.Errorf("Piglet fields cannot be null: %s", strings.Join(fields, ", "))
	}
	if !pigletNamePattern.MatchString(p.Name) {
		return fmt.Errorf("piglet name %q must start with an alphanumeric character and contain only alphanumerics, '.', '_', or '-'", p.Name)
	}
	if err := validateToolAllowlist("tools", p.BuiltinTools, agenttools.BuiltinToolNames()); err != nil {
		return err
	}
	if err := validateDiscovery(p.Discovery); err != nil {
		return err
	}
	if p.Extends != nil {
		ref, err := validateTypedSource(p.Extends.Source, sourceref.BareReject)
		if err != nil {
			return fmt.Errorf("extends.source: %w", err)
		}
		if ref.Kind == sourceref.KindContributed && !installresolver.SupportsSourceScheme(ref.Scheme) {
			return fmt.Errorf("extends.source: source scheme %q has no installed resolver", ref.Scheme)
		}
		if p.Extends.Version != strings.TrimSpace(p.Extends.Version) {
			return fmt.Errorf("extends.version must not have surrounding whitespace")
		}
	}
	packageAliases := make(map[string]struct{}, len(p.Packages))
	for _, alias := range slices.Sorted(maps.Keys(p.Packages)) {
		source := p.Packages[alias]
		if !pigletNamePattern.MatchString(alias) {
			return fmt.Errorf("packages[%q]: alias must start with an alphanumeric character and contain only alphanumerics, '.', '_', or '-'", alias)
		}
		if ref, err := validateTypedSource(source, sourceref.BareReject); err != nil {
			return fmt.Errorf("packages[%q]: %w", alias, err)
		} else if ref.Kind == sourceref.KindContributed && !installresolver.SupportsSourceScheme(ref.Scheme) {
			return fmt.Errorf("packages[%q]: source scheme %q has no installed resolver", alias, ref.Scheme)
		}
		packageAliases[alias] = struct{}{}
	}
	extensionNames := make(map[string]struct{}, len(p.Extensions))
	for i, ext := range p.Extensions {
		if ext.Name == "" {
			return fmt.Errorf("extensions[%d]: name is required", i)
		}
		if _, exists := extensionNames[ext.Name]; exists {
			return fmt.Errorf("extensions[%d]: duplicate name %q", i, ext.Name)
		}
		extensionNames[ext.Name] = struct{}{}
		if err := validateToolAllowlist(fmt.Sprintf("extensions[%d].tools", i), ext.Tools, nil); err != nil {
			return err
		}
		seenOrigins := make(map[string]struct{}, len(ext.Origins))
		for j, origin := range ext.Origins {
			if _, exists := seenOrigins[origin]; exists {
				return fmt.Errorf("extensions[%d].origins[%d] duplicates %q", i, j, origin)
			}
			seenOrigins[origin] = struct{}{}
			if err := validateOrigin(fmt.Sprintf("extensions[%d].origins[%d]", i, j), origin, packageAliases, p.Extends != nil, p.sourcePath != ""); err != nil {
				return err
			}
		}
	}
	skillNames := make(map[string]struct{}, len(p.Skills))
	for i, skill := range p.Skills {
		if skill.Name == "" {
			return fmt.Errorf("skills[%d]: name is required", i)
		}
		if _, exists := skillNames[skill.Name]; exists {
			return fmt.Errorf("skills[%d]: duplicate name %q", i, skill.Name)
		}
		skillNames[skill.Name] = struct{}{}
		seenOrigins := make(map[string]struct{}, len(skill.Origins))
		for j, origin := range skill.Origins {
			if _, exists := seenOrigins[origin]; exists {
				return fmt.Errorf("skills[%d].origins[%d] duplicates %q", i, j, origin)
			}
			seenOrigins[origin] = struct{}{}
			if err := validateOrigin(fmt.Sprintf("skills[%d].origins[%d]", i, j), origin, packageAliases, p.Extends != nil, p.sourcePath != ""); err != nil {
				return err
			}
		}
	}
	secretNames, err := validateSecretDeclarations(p.Secrets)
	if err != nil {
		return err
	}
	if err := validateAgentEnvironment(p.AgentEnv); err != nil {
		return err
	}
	if p.AgentEnv != nil {
		targets := map[string]bool{}
		for i, binding := range p.AgentEnv.Secrets {
			if _, exists := secretNames[binding.SecretRef]; !exists {
				return fmt.Errorf("agentEnv.secrets[%d].secretRef %q is not declared", i, binding.SecretRef)
			}
			if strings.TrimSpace(binding.Target.Env) == "" || !environmentNamePattern.MatchString(binding.Target.Env) {
				return fmt.Errorf("agentEnv.secrets[%d].target.env %q is invalid", i, binding.Target.Env)
			}
			if targets[binding.Target.Env] {
				return fmt.Errorf("agentEnv.secrets[%d].target.env duplicates %q", i, binding.Target.Env)
			}
			targets[binding.Target.Env] = true
		}
	}
	if err := validateBuildSpec(p.Build); err != nil {
		return err
	}
	if err := validateReleaseSpec(p.Release); err != nil {
		return err
	}
	return nil
}

func validateToolAllowlist(location string, values *[]string, allowed []string) error {
	if values == nil {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(*values))
	for i, name := range *values {
		if name == "" || name != strings.TrimSpace(name) {
			return fmt.Errorf("%s[%d] must be a non-empty name without surrounding whitespace", location, i)
		}
		if len(allowedSet) > 0 {
			if _, ok := allowedSet[name]; !ok {
				return fmt.Errorf("%s[%d] names unknown built-in tool %q", location, i, name)
			}
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%s[%d] duplicates %q", location, i, name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateDiscovery(discovery *Discovery) error {
	if discovery == nil {
		return nil
	}
	for _, entry := range []struct {
		kind    string
		sources []string
	}{
		{kind: "extensions", sources: discovery.Extensions},
		{kind: "skills", sources: discovery.Skills},
	} {
		seen := make(map[string]struct{}, len(entry.sources))
		for i, source := range entry.sources {
			if source != "workspace" && source != "user" {
				return fmt.Errorf("discovery.%s[%d] must be workspace or user, got %q", entry.kind, i, source)
			}
			if _, exists := seen[source]; exists {
				return fmt.Errorf("discovery.%s[%d] duplicates %q", entry.kind, i, source)
			}
			seen[source] = struct{}{}
		}
	}
	return nil
}

// validateReleaseSpec checks the Piglet release identity. release.version is
// the distributed artifact SemVer, kept separate from portable build defaults.
func validateReleaseSpec(release *ReleaseSpec) error {
	if release == nil {
		return nil
	}
	if release.Version != "" && !semver.IsValid("v"+release.Version) {
		return fmt.Errorf("release.version %q must be valid SemVer", release.Version)
	}
	return nil
}

func validateAgentEnvironment(environment *AgentEnvironment) error {
	if environment == nil {
		return nil
	}
	forms := 0
	for _, value := range []string{environment.Image, environment.DevContainer, environment.Source} {
		if strings.TrimSpace(value) != "" {
			forms++
		}
	}
	if forms != 1 {
		return fmt.Errorf("agentEnv requires exactly one of image, devContainer, or source")
	}
	if environment.Image != "" {
		if strings.TrimSpace(environment.Image) != environment.Image || strings.ContainsAny(environment.Image, " \t\r\n") || strings.Contains(environment.Image, "://") {
			return fmt.Errorf("agentEnv.image must be a non-empty OCI image reference without whitespace or URL scheme")
		}
	}
	if environment.DevContainer != "" {
		path := strings.TrimPrefix(environment.DevContainer, "workspace:")
		if strings.HasPrefix(path, "piglet:") {
			return fmt.Errorf("agentEnv.devContainer is workspace:-anchored and cannot use piglet:")
		}
		volume := pigletWindowsPathPattern.MatchString(path)
		clean := filepath.Clean(filepath.FromSlash(path))
		base := filepath.Base(clean)
		if filepath.IsAbs(path) || strings.HasPrefix(path, "/") || volume || strings.Contains(path, `\`) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || (base != "devcontainer.json" && base != ".devcontainer.json") {
			return fmt.Errorf("agentEnv.devContainer must be a portable relative workspace: path to devcontainer.json or .devcontainer.json")
		}
	}
	if environment.Source != "" {
		ref, err := validateTypedSource(environment.Source, sourceref.BareNPM)
		if err != nil {
			return fmt.Errorf("agentEnv.source: %w", err)
		}
		if ref.Kind == sourceref.KindContributed && !installresolver.SupportsSourceScheme(ref.Scheme) {
			return fmt.Errorf("agentEnv.source: source scheme %q has no installed resolver", ref.Scheme)
		}
	}
	if environment.PigRuntime != nil {
		if environment.PigRuntime.Mode != "" && environment.PigRuntime.Mode != "inject" && environment.PigRuntime.Mode != "image" {
			return fmt.Errorf("agentEnv.pigRuntime.mode must be inject or image")
		}
		if strings.ContainsAny(environment.PigRuntime.Version, " \t\r\n") {
			return fmt.Errorf("agentEnv.pigRuntime.version must not contain whitespace")
		}
	}
	if environment.Policy != nil && environment.Policy.Preset != "" && environment.Policy.Preset != "minimal" && environment.Policy.Preset != "standard" && environment.Policy.Preset != "elevated" {
		return fmt.Errorf("agentEnv.policy.preset must be minimal, standard, or elevated")
	}
	if err := validateAgentWorkspace(environment.Workspace); err != nil {
		return err
	}
	if err := validateAgentMounts(environment); err != nil {
		return err
	}
	return nil
}

func validateAgentWorkspace(workspace *AgentWorkspace) error {
	if workspace == nil || workspace.Folder == "" {
		return nil
	}
	if err := validateContainerPath("agentEnv.workspace.folder", workspace.Folder); err != nil {
		return err
	}
	return nil
}

func validateAgentMounts(environment *AgentEnvironment) error {
	if len(environment.Mounts) == 0 {
		return nil
	}
	preset := "standard"
	if environment.Policy != nil && environment.Policy.Preset != "" {
		preset = environment.Policy.Preset
	}
	if preset != "elevated" {
		return fmt.Errorf("agentEnv.mounts require policy.preset: elevated")
	}
	reserved := reservedContainerTargets(environment)
	seen := make(map[string]struct{}, len(environment.Mounts))
	for i, mount := range environment.Mounts {
		if strings.TrimSpace(mount.Source) == "" {
			return fmt.Errorf("agentEnv.mounts[%d].source is required", i)
		}
		if err := validateContainerPath(fmt.Sprintf("agentEnv.mounts[%d].target", i), mount.Target); err != nil {
			return err
		}
		target := cleanContainerPath(mount.Target)
		if _, exists := seen[target]; exists {
			return fmt.Errorf("agentEnv.mounts[%d]: duplicate target %q", i, target)
		}
		seen[target] = struct{}{}
		for _, reservedTarget := range reserved {
			if containerPathsOverlap(target, reservedTarget) {
				return fmt.Errorf("agentEnv.mounts[%d].target %q overlaps the reserved path %q", i, target, reservedTarget)
			}
		}
	}
	return nil
}

// reservedContainerTargets lists mount destinations Pig owns and an elevated
// Piglet mount must not shadow.
func reservedContainerTargets(environment *AgentEnvironment) []string {
	folder := "/workspace"
	if environment.Workspace != nil && environment.Workspace.Folder != "" {
		folder = cleanContainerPath(environment.Workspace.Folder)
	}
	return []string{folder, "/home/pig", "/run/pig", "/tmp", "/proc", "/sys", "/dev", "/etc"}
}

func validateContainerPath(field, raw string) error {
	if strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, " \t\r\n") {
		return fmt.Errorf("%s %q must not contain whitespace", field, raw)
	}
	if !strings.HasPrefix(raw, "/") {
		return fmt.Errorf("%s %q must be an absolute container path", field, raw)
	}
	clean := cleanContainerPath(raw)
	if clean == "/" || clean != raw || strings.Contains(raw, "..") {
		return fmt.Errorf("%s %q must be a normalized absolute path other than /", field, raw)
	}
	return nil
}

// containerPathsOverlap reports whether either container path is the other or
// contains it, so a mount cannot shadow or be shadowed by a reserved path.
func containerPathsOverlap(a, b string) bool {
	a = cleanContainerPath(a)
	b = cleanContainerPath(b)
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func validateBuildSpec(build *BuildSpec) error {
	if build == nil {
		return nil
	}
	seenTargets := make(map[string]struct{}, len(build.Targets))
	for i, target := range build.Targets {
		goos, goarch, ok := strings.Cut(target, "/")
		if !ok || !pigletTargetPartPattern.MatchString(goos) || !pigletTargetPartPattern.MatchString(goarch) {
			return fmt.Errorf("build.targets[%d] %q must be os/arch", i, target)
		}
		if _, exists := seenTargets[target]; exists {
			return fmt.Errorf("build.targets[%d] duplicates %q", i, target)
		}
		seenTargets[target] = struct{}{}
	}
	if build.ExtensionRealization != "" && build.ExtensionRealization != "fused" {
		return fmt.Errorf("build.extensionRealization %q must be fused", build.ExtensionRealization)
	}
	if build.OutputName != "" {
		if build.OutputName == "." || build.OutputName == ".." || filepath.Base(build.OutputName) != build.OutputName || strings.ContainsAny(build.OutputName, `/\\`) {
			return fmt.Errorf("build.outputName %q must be a binary basename", build.OutputName)
		}
	}
	return nil
}

func validateSecretDeclarations(declarations []SecretDeclaration) (map[string]struct{}, error) {
	names := make(map[string]struct{}, len(declarations))
	for i, declaration := range declarations {
		name := strings.TrimSpace(declaration.Name)
		if !pigletNamePattern.MatchString(name) {
			return nil, fmt.Errorf("secrets[%d].name %q is invalid", i, declaration.Name)
		}
		if _, exists := names[name]; exists {
			return nil, fmt.Errorf("secrets[%d]: duplicate name %q", i, name)
		}
		names[name] = struct{}{}
		arms := 0
		if declaration.From.Env != "" {
			arms++
			if !environmentNamePattern.MatchString(declaration.From.Env) {
				return nil, fmt.Errorf("secrets[%d].from.env %q is invalid", i, declaration.From.Env)
			}
		}
		if declaration.From.File != "" {
			arms++
			if !filepath.IsAbs(declaration.From.File) && !strings.HasPrefix(declaration.From.File, "~/") {
				return nil, fmt.Errorf("secrets[%d].from.file must be absolute or home-relative machine-local path", i)
			}
		}
		if declaration.From.Ref != "" {
			arms++
			if !secretResolverRefPattern.MatchString(declaration.From.Ref) {
				return nil, fmt.Errorf("secrets[%d].from.ref must be <resolver>:<opaque-id>", i)
			}
		}
		if arms != 1 {
			return nil, fmt.Errorf("secrets[%d].from requires exactly one of env, file, or ref", i)
		}
	}
	return names, nil
}

func validateOrigin(location, origin string, packageAliases map[string]struct{}, allowInheritedPackage, allowResolvedLocal bool) error {
	if origin == "" || origin != strings.TrimSpace(origin) {
		return fmt.Errorf("%s must be a non-empty typed source without surrounding whitespace", location)
	}
	if alias, ok := strings.CutPrefix(origin, "package:"); ok {
		if !pigletNamePattern.MatchString(alias) {
			return fmt.Errorf("%s package alias %q is invalid", location, alias)
		}
		if _, exists := packageAliases[alias]; !exists && !allowInheritedPackage {
			return fmt.Errorf("%s package %q is not declared", location, alias)
		}
		return nil
	}
	ref, err := validateTypedSource(origin, sourceref.BareReject)
	if err != nil {
		return fmt.Errorf("%s: %w", location, err)
	}
	if ref.Kind == sourceref.KindLocal {
		if !strings.HasPrefix(origin, "local:") {
			return fmt.Errorf("%s local source must use local:<relative-path>", location)
		}
		if filepath.IsAbs(ref.Locator) && allowResolvedLocal {
			return nil
		}
		if _, err := portablePathIdentity(ref.Locator); err != nil {
			return fmt.Errorf("%s: %w", location, err)
		}
	}
	if ref.Kind == sourceref.KindContributed && !installresolver.SupportsSourceScheme(ref.Scheme) {
		return fmt.Errorf("%s source scheme %q has no installed resolver", location, ref.Scheme)
	}
	return nil
}

func validateTypedSource(raw string, bare sourceref.BarePolicy) (sourceref.Ref, error) {
	return sourceref.Parse(raw, sourceref.Options{Bare: bare, AllowContributed: true})
}
