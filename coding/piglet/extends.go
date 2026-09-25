package piglet

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	semver "github.com/Masterminds/semver/v3"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
)

const maxExtendsDepth = 8

// LineageEntry pins one source in an effective Piglet's inheritance chain.
type LineageEntry struct {
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	Digest  string `json:"digest"`
}

// EffectiveResolution is a source Piglet resolved through its exact base
// lineage. Piglet contains no release/build/extends/remove metadata.
type EffectiveResolution struct {
	Piglet          *Piglet        `json:"-"`
	Lineage         []LineageEntry `json:"lineage"`
	SourceDigest    string         `json:"sourceDigest"`
	EffectiveDigest string         `json:"effectiveDigest"`
	GraphDigest     string         `json:"graphDigest"`
}

// ResolutionIdentity returns copies of the exact lineage and effective/graph
// digests attached by ResolveEffective. Source-only Piglets return zero values.
func (p *Piglet) ResolutionIdentity() ([]LineageEntry, string, string) {
	return slices.Clone(p.lineage), p.effectiveDigest, p.graphDigest
}

type lineageVisit struct {
	path   string
	digest string
}

// ResolveOptions supplies machine-local anchors that are not portable Piglet
// identity.
type ResolveOptions struct {
	Workspace string
}

// ResolveEffective resolves with no workspace anchor. Piglets that require a
// workspace:-anchored field fail until the caller supplies one explicitly.
func ResolveEffective(path string) (*EffectiveResolution, error) {
	return ResolveEffectiveWithOptions(path, ResolveOptions{})
}

// ResolveEffectiveWithOptions resolves path's typed extends lineage and
// deterministic merge algebra. Each source's Piglet-owned local paths are
// anchored before merge, so inherited resources are never re-anchored to a
// child directory.
//
// pig additive (D18): additive Piglet derivation and exact lineage.
func ResolveEffectiveWithOptions(path string, options ResolveOptions) (*EffectiveResolution, error) {
	canonical, err := canonicalPigletPath(path)
	if err != nil {
		return nil, err
	}
	return resolveEffectiveRoot(canonical, options)
}

func resolveEffectiveRoot(canonical string, options ResolveOptions) (*EffectiveResolution, error) {
	resolved, err := resolveEffective(canonical, "local:"+filepath.ToSlash(filepath.Base(canonical)), nil, 0, options)
	if err != nil {
		return nil, err
	}
	var graph strings.Builder
	for _, entry := range resolved.Lineage {
		_, _ = fmt.Fprintf(&graph, "%s\x00%s\x00%s\n", entry.Source, entry.Version, entry.Digest)
	}
	resolved.GraphDigest = digestPigletBytes([]byte(graph.String()))
	resolved.EffectiveDigest = digestPigletBytes([]byte("effective-v1\x00" + resolved.GraphDigest))
	resolved.Piglet.lineage = slices.Clone(resolved.Lineage)
	resolved.Piglet.effectiveDigest = resolved.EffectiveDigest
	resolved.Piglet.graphDigest = resolved.GraphDigest
	return resolved, nil
}

func resolveEffective(path, sourceIdentity string, visits []lineageVisit, depth int, options ResolveOptions) (*EffectiveResolution, error) {
	if depth > maxExtendsDepth {
		return nil, fmt.Errorf("Piglet extends depth exceeds %d: %s", maxExtendsDepth, visitChain(visits, path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Piglet %s: %w", path, err)
	}
	digest := digestPigletBytes(data)
	for _, visit := range visits {
		if visit.path == path && visit.digest == digest {
			return nil, fmt.Errorf("Piglet extends cycle: %s", visitChain(visits, path))
		}
	}
	p, err := ParseBytes(data)
	if err != nil {
		return nil, fmt.Errorf("Piglet %s: %w", path, err)
	}
	p.sourcePath = path
	p.sourceDir = filepath.Dir(path)
	if err := anchorPigletLocalPaths(p, path, options.Workspace); err != nil {
		return nil, fmt.Errorf("Piglet %s paths: %w", path, err)
	}
	entry := LineageEntry{Source: sourceIdentity, Digest: digest}
	if p.Release != nil {
		entry.Version = p.Release.Version
	}
	visits = append(slices.Clone(visits), lineageVisit{path: path, digest: digest})

	if p.Extends == nil {
		effective := clearDerivationMetadata(p)
		if err := effective.Validate(); err != nil {
			return nil, fmt.Errorf("effective Piglet %s: %w", path, err)
		}
		return &EffectiveResolution{Piglet: effective, Lineage: []LineageEntry{entry}, SourceDigest: digest}, nil
	}
	basePath, identity, err := resolveExtendsSource(path, p.Extends.Source)
	if err != nil {
		return nil, fmt.Errorf("Piglet %s extends.source: %w", path, err)
	}
	base, err := resolveEffective(basePath, identity, visits, depth+1, options)
	if err != nil {
		return nil, err
	}
	if err := checkExtendsVersion(p.Extends.Version, base.Lineage[len(base.Lineage)-1].Version, identity); err != nil {
		return nil, err
	}
	effective, err := mergePiglets(base.Piglet, p)
	if err != nil {
		return nil, fmt.Errorf("derive Piglet %s from %s: %w", path, basePath, err)
	}
	if err := effective.Validate(); err != nil {
		return nil, fmt.Errorf("effective Piglet %s: %w", path, err)
	}
	return &EffectiveResolution{
		Piglet: effective, Lineage: append(slices.Clone(base.Lineage), entry), SourceDigest: digest,
	}, nil
}

func canonicalPigletPath(path string) (string, error) {
	path = expandOriginPath("", path)
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve Piglet path %q: %w", path, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve Piglet path %q: %w", path, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("Piglet source %q is a directory; expected a Piglet YAML file", canonical)
	}
	return canonical, nil
}

func resolveExtendsSource(childPath, raw string) (string, string, error) {
	ref, err := sourceref.Parse(raw, sourceref.Options{BaseDir: filepath.Dir(childPath), Bare: sourceref.BareReject, AllowContributed: true})
	if err != nil {
		return "", "", err
	}
	var path, identity string
	switch ref.Kind {
	case sourceref.KindLocal:
		if filepath.IsAbs(ref.Locator) || strings.HasPrefix(ref.Locator, "~/") || pigletWindowsPathPattern.MatchString(ref.Locator) {
			return "", "", fmt.Errorf("local Piglet source %q must be relative", raw)
		}
		identity = "local:" + filepath.ToSlash(filepath.Clean(ref.Locator))
		path = expandOriginPath(filepath.Dir(childPath), ref.Locator)
	case sourceref.KindContributed:
		identity, err = ref.Identity(filepath.Dir(childPath))
		if err != nil {
			return "", "", err
		}
		path, err = installresolver.ResolvePigletSource(filepath.Dir(childPath), raw)
		if err != nil {
			return "", "", err
		}
	default:
		return "", "", fmt.Errorf("source kind %q is not supported for Piglet extends", ref.Kind)
	}
	canonical, err := canonicalPigletPath(path)
	return canonical, identity, err
}

func checkExtendsVersion(constraint, version, source string) error {
	if constraint == "" {
		return nil
	}
	if version == "" {
		return fmt.Errorf("extends source %s has no release.version required by constraint %q", source, constraint)
	}
	rangeConstraint, err := semver.NewConstraint(constraint)
	if err != nil {
		return fmt.Errorf("extends.version %q is invalid: %w", constraint, err)
	}
	resolved, err := semver.NewVersion(version)
	if err != nil {
		return fmt.Errorf("extends source %s has invalid release.version %q: %w", source, version, err)
	}
	if !rangeConstraint.Check(resolved) {
		return fmt.Errorf("extends source %s resolved version %s does not satisfy %q", source, version, constraint)
	}
	return nil
}

func clearDerivationMetadata(p *Piglet) *Piglet {
	p.Extends = nil
	p.Release = nil
	p.Build = nil
	p.present = nil
	p.nullFields = nil
	return p
}

func mergePiglets(base, child *Piglet) (*Piglet, error) {
	result := clonePiglet(base)
	if err := applyRemovals(result, child.Extends.Remove); err != nil {
		return nil, err
	}
	if err := rejectUnsafeWidening(base, child); err != nil {
		return nil, err
	}
	result.Name = child.Name
	if child.present["description"] {
		result.Description = child.Description
	}
	if child.present["tools"] {
		result.BuiltinTools = cloneStringList(child.BuiltinTools)
	}
	if child.present["discovery"] {
		result.Discovery = child.Discovery
	}
	if child.present["systemPrompt"] {
		result.SystemPrompt = child.SystemPrompt
	}
	if child.present["agentEnv"] {
		result.AgentEnv = child.AgentEnv
		result.devContainerPath = child.devContainerPath
	}
	if child.present["model"] {
		result.Model = child.Model
	}
	if child.present["secrets"] {
		result.Secrets = slices.Clone(child.Secrets)
	}

	if result.Packages == nil {
		result.Packages = map[string]string{}
	}
	maps.Copy(result.Packages, child.Packages)
	var err error
	result.Extensions, err = mergeNamed(result.Extensions, child.Extensions, func(v ExtensionEntry) string { return v.Name })
	if err != nil {
		return nil, err
	}
	result.Skills, err = mergeNamed(result.Skills, child.Skills, func(v SkillEntry) string { return v.Name })
	if err != nil {
		return nil, err
	}
	if err := rejectReplacedPackageDependencies(base, child, result); err != nil {
		return nil, err
	}
	result.sourcePath = child.sourcePath
	result.sourceDir = child.sourceDir
	result.workspaceRoot = child.workspaceRoot
	return clearDerivationMetadata(result), nil
}

func mergeNamed[T any](base, child []T, identity func(T) string) ([]T, error) {
	result := slices.Clone(base)
	positions := make(map[string]int, len(result))
	for i, item := range result {
		positions[identity(item)] = i
	}
	seenChild := map[string]bool{}
	for _, item := range child {
		id := identity(item)
		if seenChild[id] {
			return nil, fmt.Errorf("duplicate child identity %q", id)
		}
		seenChild[id] = true
		if position, exists := positions[id]; exists {
			result[position] = item
			continue
		}
		positions[id] = len(result)
		result = append(result, item)
	}
	return result, nil
}

func applyRemovals(p *Piglet, remove *RemoveSpec) error {
	if remove == nil {
		return nil
	}
	for _, alias := range remove.Packages {
		if _, exists := p.Packages[alias]; !exists {
			return fmt.Errorf("remove.packages: %q is absent", alias)
		}
		delete(p.Packages, alias)
	}
	var err error
	p.Extensions, err = removeNamed(p.Extensions, remove.Extensions, func(v ExtensionEntry) string { return v.Name }, "extensions")
	if err != nil {
		return err
	}
	p.Skills, err = removeNamed(p.Skills, remove.Skills, func(v SkillEntry) string { return v.Name }, "skills")
	return err
}

func removeNamed[T any](items []T, removes []string, identity func(T) string, label string) ([]T, error) {
	if len(removes) == 0 {
		return items, nil
	}
	removeSet := map[string]bool{}
	for _, raw := range removes {
		id := raw
		if removeSet[id] {
			return nil, fmt.Errorf("remove.%s: duplicate identity %q", label, raw)
		}
		removeSet[id] = true
	}
	found := map[string]bool{}
	out := make([]T, 0, len(items))
	for _, item := range items {
		id := identity(item)
		if removeSet[id] {
			found[id] = true
			continue
		}
		out = append(out, item)
	}
	for id := range removeSet {
		if !found[id] {
			return nil, fmt.Errorf("remove.%s: %q is absent", label, id)
		}
	}
	return out, nil
}

func rejectReplacedPackageDependencies(base, child, effective *Piglet) error {
	replaced := map[string]bool{}
	for alias, source := range child.Packages {
		if old, exists := base.Packages[alias]; exists && old != source {
			replaced[alias] = true
		}
	}
	if len(replaced) == 0 {
		return nil
	}
	childExtensions := map[string]bool{}
	for _, extension := range child.Extensions {
		childExtensions[extension.Name] = true
	}
	childSkills := map[string]bool{}
	for _, skill := range child.Skills {
		childSkills[skill.Name] = true
	}
	for _, extension := range effective.Extensions {
		if childExtensions[extension.Name] {
			continue
		}
		for _, origin := range extension.Origins {
			alias, isPackage := strings.CutPrefix(origin, "package:")
			if isPackage && replaced[alias] {
				return fmt.Errorf("package %q replacement leaves inherited extension %q referring to the old alias", alias, extension.Name)
			}
		}
	}
	for _, skill := range effective.Skills {
		if childSkills[skill.Name] {
			continue
		}
		for _, origin := range skill.Origins {
			alias, isPackage := strings.CutPrefix(origin, "package:")
			if isPackage && replaced[alias] {
				return fmt.Errorf("package %q replacement leaves inherited skill %q referring to the old alias", alias, skill.Name)
			}
		}
	}
	return nil
}

func rejectUnsafeWidening(base, child *Piglet) error {
	if child.Extends != nil && child.Extends.AllowWiden {
		return nil
	}
	var widened []string
	if child.present["tools"] && allowlistWidens(base.BuiltinTools, child.BuiltinTools) {
		widened = append(widened, "tools")
	}
	if child.present["discovery"] && discoveryWidens(base.Discovery, child.Discovery) {
		widened = append(widened, "discovery")
	}
	if child.present["agentEnv"] && agentEnvironmentWidens(base.AgentEnv, child.AgentEnv) {
		widened = append(widened, "agentEnv")
	}
	baseExtensions := map[string]ExtensionEntry{}
	for _, extension := range base.Extensions {
		baseExtensions[extension.Name] = extension
	}
	for _, extension := range child.Extensions {
		if previous, exists := baseExtensions[extension.Name]; exists && extensionWidens(previous, extension) {
			widened = append(widened, "extension/"+extension.Name)
		}
	}
	if len(widened) > 0 {
		slices.Sort(widened)
		return fmt.Errorf("capability widening %v requires extends.allowWiden: true", widened)
	}
	return nil
}

func extensionWidens(base, child ExtensionEntry) bool {
	return allowlistWidens(base.Tools, child.Tools)
}

func allowlistWidens(base, child *[]string) bool {
	if base == nil {
		return false
	}
	if child == nil {
		return true
	}
	for _, name := range *child {
		if !slices.Contains(*base, name) {
			return true
		}
	}
	return false
}

func discoveryWidens(base, child *Discovery) bool {
	var baseExtensions, baseSkills []string
	if base != nil {
		baseExtensions = base.Extensions
		baseSkills = base.Skills
	}
	if child == nil {
		return false
	}
	return listAdds(child.Extensions, baseExtensions) || listAdds(child.Skills, baseSkills)
}

func listAdds(child, base []string) bool {
	for _, value := range child {
		if !slices.Contains(base, value) {
			return true
		}
	}
	return false
}

func agentEnvironmentWidens(base, child *AgentEnvironment) bool {
	if base == nil {
		return false
	}
	if child == nil {
		return false
	}
	if policyRank(child) > policyRank(base) {
		return true
	}
	if base.Workspace != nil && base.Workspace.ReadOnly != nil && *base.Workspace.ReadOnly {
		if child.Workspace == nil || child.Workspace.ReadOnly == nil || !*child.Workspace.ReadOnly {
			return true
		}
	}
	baseMounts := map[string]AgentMount{}
	for _, mount := range base.Mounts {
		baseMounts[mount.Target] = mount
	}
	for _, mount := range child.Mounts {
		previous, exists := baseMounts[mount.Target]
		if !exists || previous.Source != mount.Source || (previous.ReadOnly && !mount.ReadOnly) {
			return true
		}
	}
	return false
}

func policyRank(environment *AgentEnvironment) int {
	preset := "standard"
	if environment != nil && environment.Policy != nil && environment.Policy.Preset != "" {
		preset = environment.Policy.Preset
	}
	switch preset {
	case "minimal":
		return 0
	case "standard":
		return 1
	case "elevated":
		return 2
	default:
		return 3
	}
}

func anchorPigletLocalPaths(p *Piglet, path, workspace string) error {
	base := filepath.Dir(path)
	if workspace != "" {
		canonicalWorkspace, err := canonicalDirectory(workspace)
		if err != nil {
			return fmt.Errorf("workspace anchor: %w", err)
		}
		p.workspaceRoot = canonicalWorkspace
	}
	for _, alias := range slices.Sorted(maps.Keys(p.Packages)) {
		anchored, err := anchorTypedLocalSource(p.Packages[alias], base, sourceref.BareReject)
		if err != nil {
			return fmt.Errorf("packages[%q]: %w", alias, err)
		}
		p.Packages[alias] = anchored
	}
	for i := range p.Extensions {
		if err := anchorOrigins(p.Extensions[i].Origins, base); err != nil {
			return fmt.Errorf("extensions[%d]: %w", i, err)
		}
	}
	for i := range p.Skills {
		if err := anchorOrigins(p.Skills[i].Origins, base); err != nil {
			return fmt.Errorf("skills[%d]: %w", i, err)
		}
	}
	if p.SystemPrompt != nil && p.SystemPrompt.File != "" {
		anchored, err := anchorPigletPath(base, p.SystemPrompt.File)
		if err != nil {
			return fmt.Errorf("systemPrompt.file: %w", err)
		}
		p.SystemPrompt.File = anchored
	}
	if p.AgentEnv != nil && p.AgentEnv.Source != "" {
		anchored, err := anchorTypedLocalSource(p.AgentEnv.Source, base, sourceref.BareNPM)
		if err != nil {
			return fmt.Errorf("agentEnv.source: %w", err)
		}
		p.AgentEnv.Source = anchored
	}
	if p.AgentEnv != nil && p.AgentEnv.DevContainer != "" {
		if p.workspaceRoot == "" {
			return fmt.Errorf("agentEnv.devContainer requires a workspace anchor; pass --workspace or run from an unambiguous workspace")
		}
		resolved, err := anchorWorkspacePath(p.workspaceRoot, p.AgentEnv.DevContainer)
		if err != nil {
			return fmt.Errorf("agentEnv.devContainer: %w", err)
		}
		if err := packagecontent.ValidateMember(p.workspaceRoot, packagecontent.AgentEnvironments, resolved); err != nil {
			return fmt.Errorf("agentEnv.devContainer: %w", err)
		}
		p.devContainerPath = resolved
	}
	return nil
}

func anchorOrigins(origins []string, base string) error {
	for i, origin := range origins {
		if strings.HasPrefix(origin, "package:") {
			continue
		}
		anchored, err := anchorTypedLocalSource(origin, base, sourceref.BareReject)
		if err != nil {
			return fmt.Errorf("origins[%d]: %w", i, err)
		}
		origins[i] = anchored
	}
	return nil
}

func anchorTypedLocalSource(raw, base string, bare sourceref.BarePolicy) (string, error) {
	ref, err := sourceref.Parse(raw, sourceref.Options{BaseDir: base, Bare: bare, AllowContributed: true})
	if err != nil {
		return "", err
	}
	if ref.Kind != sourceref.KindLocal {
		return raw, nil
	}
	anchored, err := anchorPigletPath(base, ref.Locator)
	if err != nil {
		return "", err
	}
	return "local:" + anchored, nil
}

func anchorWorkspacePath(workspace, raw string) (string, error) {
	path := strings.TrimSpace(raw)
	path = strings.TrimPrefix(path, "workspace:")
	if strings.HasPrefix(path, "piglet:") {
		return "", fmt.Errorf("%q uses piglet: in a workspace:-anchored field", raw)
	}
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "~/") || pigletWindowsPathPattern.MatchString(path) {
		return "", fmt.Errorf("%q must be a relative workspace: path", raw)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q escapes the workspace anchor", raw)
	}
	target := filepath.Join(workspace, clean)
	if err := ensureNoSymlinkEscape(workspace, target); err != nil {
		return "", fmt.Errorf("%q: %w", raw, err)
	}
	return target, nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(expandOriginPath("", path))
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	return canonical, nil
}

func anchorPigletPath(base, raw string) (string, error) {
	clean, err := portablePathIdentity(raw)
	if err != nil {
		return "", err
	}
	target := filepath.Join(base, filepath.FromSlash(clean))
	if err := ensureNoSymlinkEscape(base, target); err != nil {
		return "", fmt.Errorf("%q: %w", raw, err)
	}
	return target, nil
}

func portablePathIdentity(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if strings.HasPrefix(path, "workspace:") {
		return "", fmt.Errorf("%q uses workspace: in a piglet:-anchored field", raw)
	}
	path = strings.TrimPrefix(path, "piglet:")
	if path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "~/") || pigletWindowsPathPattern.MatchString(path) {
		return "", fmt.Errorf("%q must be a relative piglet: path", raw)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q escapes the Piglet anchor", raw)
	}
	return filepath.ToSlash(clean), nil
}

func ensureNoSymlinkEscape(base, target string) error {
	ancestor := target
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return fmt.Errorf("cannot resolve existing path ancestor")
		}
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("resolves outside the Piglet anchor")
	}
	return nil
}

// Clone returns an independent Piglet value while preserving its resolved
// source anchors. Runtime caches and locks are intentionally reset.
func Clone(p *Piglet) *Piglet {
	if p == nil {
		return nil
	}
	return clonePiglet(p)
}

func clonePiglet(p *Piglet) *Piglet {
	var extends *ExtendsSpec
	if p.Extends != nil {
		value := *p.Extends
		if p.Extends.Remove != nil {
			remove := *p.Extends.Remove
			remove.Packages = slices.Clone(remove.Packages)
			remove.Extensions = slices.Clone(remove.Extensions)
			remove.Skills = slices.Clone(remove.Skills)
			value.Remove = &remove
		}
		extends = &value
	}
	extensions := slices.Clone(p.Extensions)
	for i := range extensions {
		extensions[i].Origins = slices.Clone(extensions[i].Origins)
		extensions[i].Tools = cloneStringList(extensions[i].Tools)
	}
	skills := slices.Clone(p.Skills)
	for i := range skills {
		skills[i].Origins = slices.Clone(skills[i].Origins)
	}
	var discovery *Discovery
	if p.Discovery != nil {
		value := *p.Discovery
		value.Extensions = slices.Clone(value.Extensions)
		value.Skills = slices.Clone(value.Skills)
		discovery = &value
	}
	var prompt *PromptRef
	if p.SystemPrompt != nil {
		value := *p.SystemPrompt
		prompt = &value
	}
	var environment *AgentEnvironment
	if p.AgentEnv != nil {
		value := *p.AgentEnv
		if value.PigRuntime != nil {
			runtime := *value.PigRuntime
			value.PigRuntime = &runtime
		}
		if value.Policy != nil {
			policy := *value.Policy
			value.Policy = &policy
		}
		if value.Workspace != nil {
			workspace := *value.Workspace
			if workspace.ReadOnly != nil {
				readOnly := *workspace.ReadOnly
				workspace.ReadOnly = &readOnly
			}
			value.Workspace = &workspace
		}
		value.Mounts = slices.Clone(value.Mounts)
		value.Secrets = slices.Clone(value.Secrets)
		environment = &value
	}
	var model *ModelConfig
	if p.Model != nil {
		value := *p.Model
		model = &value
	}
	var build *BuildSpec
	if p.Build != nil {
		value := *p.Build
		value.Targets = slices.Clone(value.Targets)
		build = &value
	}
	var release *ReleaseSpec
	if p.Release != nil {
		value := *p.Release
		release = &value
	}
	return &Piglet{
		Name: p.Name, Description: p.Description,
		Extends: extends, BuiltinTools: cloneStringList(p.BuiltinTools), Packages: maps.Clone(p.Packages), Extensions: extensions, Skills: skills,
		Discovery: discovery, SystemPrompt: prompt, AgentEnv: environment, Model: model,
		Build: build, Release: release, Secrets: slices.Clone(p.Secrets),
		sourceDir: p.sourceDir, sourcePath: p.sourcePath, present: maps.Clone(p.present), nullFields: maps.Clone(p.nullFields),
		lineage: slices.Clone(p.lineage), effectiveDigest: p.effectiveDigest, graphDigest: p.graphDigest,
		workspaceRoot: p.workspaceRoot, devContainerPath: p.devContainerPath,
	}
}

func cloneStringList(values *[]string) *[]string {
	if values == nil {
		return nil
	}
	cloned := slices.Clone(*values)
	return &cloned
}

func digestPigletBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func visitChain(visits []lineageVisit, final string) string {
	parts := make([]string, 0, len(visits)+1)
	for _, visit := range visits {
		parts = append(parts, visit.path)
	}
	parts = append(parts, final)
	return strings.Join(parts, " -> ")
}
