package piglet

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/installresolver"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

type packageResolution struct {
	root      string
	resources packagecontent.Resources
	err       error
}

type ResolvedPackage struct {
	Alias  string
	Source string
	Root   string
}

// SourcePath returns the parsed Piglet source path when available.
func (p *Piglet) SourcePath() string {
	return p.sourcePath
}

// ResolvePackages materializes every declared Package without mutating Package
// settings and returns its canonical root for lock/record construction.
func ResolvePackages(p *Piglet) ([]ResolvedPackage, error) {
	resolved := make([]ResolvedPackage, 0, len(p.Packages))
	for _, alias := range slices.Sorted(maps.Keys(p.Packages)) {
		result := p.resolvePackage(alias)
		if result.err != nil {
			return nil, fmt.Errorf("package %q: %w", alias, result.err)
		}
		resolved = append(resolved, ResolvedPackage{Alias: alias, Source: p.Packages[alias], Root: result.root})
	}
	return resolved, nil
}

// ResolvedAgentEnvironment identifies the concrete image or Dev Container
// selected by a Piglet environment source.
type ResolvedAgentEnvironment struct {
	Form  string
	Value string
}

// ResolveAgentEnvironment validates and materializes the optional environment.
// It does not start an engine or execute lifecycle commands.
func ResolveAgentEnvironment(p *Piglet) (*ResolvedAgentEnvironment, error) {
	if p == nil || p.AgentEnv == nil {
		return nil, nil
	}
	switch {
	case p.AgentEnv.Image != "":
		return &ResolvedAgentEnvironment{Form: "image", Value: p.AgentEnv.Image}, nil
	case p.AgentEnv.DevContainer != "":
		if p.devContainerPath == "" {
			return nil, fmt.Errorf("agentEnv.devContainer has no resolved workspace anchor")
		}
		return &ResolvedAgentEnvironment{Form: "devContainer", Value: p.devContainerPath}, nil
	case p.AgentEnv.Source != "":
		root, err := p.materialize(p.AgentEnv.Source, sourceref.BareNPM)
		if err != nil {
			return nil, fmt.Errorf("agentEnv.source: %w", err)
		}
		resources, err := packagecontent.Validate(root)
		if err != nil {
			return nil, fmt.Errorf("agentEnv.source: %w", err)
		}
		if len(resources.AgentEnvironments) != 1 {
			return nil, fmt.Errorf("agentEnv.source must resolve to exactly one agent environment, found %d", len(resources.AgentEnvironments))
		}
		return &ResolvedAgentEnvironment{Form: "source", Value: resources.AgentEnvironments[0]}, nil
	default:
		return nil, fmt.Errorf("agentEnv has no source form")
	}
}

// ResolvedExtension is an extension with its origin resolved to a concrete path.
type ResolvedExtension struct {
	Entry  ExtensionEntry
	Path   string // Resolved filesystem path
	Origin string // The first typed origin that resolved successfully.
}

// ResolvedSkill is a skill with its origin resolved to a concrete path.
type ResolvedSkill struct {
	Entry  SkillEntry
	Path   string // Resolved filesystem path
	Origin string // The first typed origin that resolved successfully.
}

// ResolveExtensions resolves all extension origins to concrete filesystem paths.
// pig additive (D18): Package and typed-source origins use the generic
// side-effect-free install materializer before member selection.
// Returns resolved extensions and any errors for unresolvable entries.
func ResolveExtensions(p *Piglet) ([]ResolvedExtension, []error) {
	var resolved []ResolvedExtension
	var errs []error

	for _, ext := range p.Extensions {
		// Scoping-only entry: name + tools but no origin. The extension is
		// loaded elsewhere (e.g. agent-server's -e flags) and this entry only
		// scopes its tools by name in ScopeTools, which never reads origin.
		// Skip it here rather than reporting it as an unresolvable load.
		if len(ext.Origins) == 0 {
			continue
		}
		path, origin, err := resolveOrigins(p, packagecontent.Extensions, ext.Name, ext.Origins)
		if err != nil {
			errs = append(errs, fmt.Errorf("extension %q: %w", ext.Name, err))
			continue
		}
		resolved = append(resolved, ResolvedExtension{Entry: ext, Path: path, Origin: origin})
	}
	return resolved, errs
}

// ResolveSkills resolves all skill origins to concrete filesystem paths.
func ResolveSkills(p *Piglet) ([]ResolvedSkill, []error) {
	var resolved []ResolvedSkill
	var errs []error

	for _, skill := range p.Skills {
		if skill.Content != "" {
			continue
		}
		path, origin, err := resolveOrigins(p, packagecontent.Skills, skill.Name, skill.Origins)
		if err != nil {
			errs = append(errs, fmt.Errorf("skill %q: %w", skill.Name, err))
			continue
		}
		resolved = append(resolved, ResolvedSkill{Entry: skill, Path: path, Origin: origin})
	}
	return resolved, errs
}

func (p *Piglet) resolvePackage(alias string) packageResolution {
	p.packageMu.Lock()
	defer p.packageMu.Unlock()
	if result, ok := p.packageResults[alias]; ok {
		return result
	}
	result := packageResolution{}
	source, exists := p.Packages[alias]
	if !exists {
		result.err = fmt.Errorf("package %q is not declared", alias)
	} else {
		result.root, result.err = p.materialize(source, sourceref.BareReject)
		if result.err == nil {
			result.resources, result.err = packagecontent.ValidatePackage(result.root)
		}
	}
	if p.packageResults == nil {
		p.packageResults = make(map[string]packageResolution)
	}
	p.packageResults[alias] = result
	return result
}

func (p *Piglet) materialize(source string, bare sourceref.BarePolicy) (string, error) {
	cwd, scope := p.materializationContext()
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: bare, AllowContributed: true})
	if err != nil {
		return "", err
	}
	if ref.Kind == sourceref.KindLocal {
		baseDir := p.sourceDir
		if baseDir == "" {
			baseDir, _ = os.Getwd()
		}
		source = expandOriginPath(canonicalPath(baseDir), ref.Locator)
	}
	return installresolver.Materialize(cwd, source, scope, io.Discard, io.Discard)
}

func (p *Piglet) materializationContext() (string, string) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = p.sourceDir
	}
	cwd = canonicalPath(cwd)
	scope := "user"
	if p.sourceDir != "" && isWithin(codingagent.ProjectConfigDir(cwd), p.sourceDir) {
		scope = "project"
	}
	return cwd, scope
}

func isWithin(root, target string) bool {
	root = canonicalPath(root)
	target = canonicalPath(target)
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func canonicalPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// resolveOrigins returns the first typed origin that resolves.
func resolveOrigins(piglet *Piglet, kind packagecontent.Kind, name string, origins []string) (string, string, error) {
	if len(origins) == 0 {
		return "", "", fmt.Errorf("no origin specified")
	}
	tried := make([]string, 0, len(origins))
	for _, origin := range origins {
		if alias, ok := strings.CutPrefix(origin, "package:"); ok {
			resolved := piglet.resolvePackage(alias)
			if resolved.err != nil {
				tried = append(tried, origin+": "+resolved.err.Error())
				continue
			}
			path, err := packagecontent.FindMember(resolved.resources, kind, name)
			if err != nil {
				tried = append(tried, origin+": "+err.Error())
				continue
			}
			return path, origin, nil
		}
		ref, err := sourceref.Parse(origin, sourceref.Options{Bare: sourceref.BareReject, AllowContributed: true})
		if err != nil {
			tried = append(tried, origin+": "+err.Error())
			continue
		}
		if ref.Kind == sourceref.KindLocal {
			path := ref.Locator
			if !filepath.IsAbs(path) {
				path = filepath.Join(piglet.sourceDir, path)
			}
			path = filepath.Clean(path)
			if _, err := os.Stat(path); err == nil {
				return path, origin, nil
			}
			tried = append(tried, origin+": unavailable")
			continue
		}
		root, err := piglet.materialize(origin, sourceref.BareReject)
		if err != nil {
			tried = append(tried, origin+": "+err.Error())
			continue
		}
		path, err := packagecontent.FindSourceMember(root, kind, name)
		if err != nil {
			tried = append(tried, origin+": "+err.Error())
			continue
		}
		return path, origin, nil
	}
	return "", "", fmt.Errorf("no origin resolved (tried: %s)", strings.Join(tried, ", "))
}

// expandPath expands ~ to the user's home directory and resolves the path.
func expandPath(path string) string {
	return expandOriginPath("", path)
}

func expandOriginPath(baseDir, path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	path = os.ExpandEnv(path)
	if baseDir != "" && !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	return filepath.Clean(path)
}
