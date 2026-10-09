package packagemanager

import (
	"fmt"
	"strings"

	sourceref "github.com/MichaelKinsy/PiG/coding/source"
)

// GitPackageSource is the host, repository path and ref of a Git package source.
type GitPackageSource struct {
	Host   string
	Path   string
	Ref    string
	Pinned bool
}

func noMatchingPackageMessage(source string, pkgs []ConfiguredPackage) string {
	if suggestion := findSuggestedConfiguredSource(source, pkgs); suggestion != "" {
		return fmt.Sprintf("No matching package found for %s. Did you mean %s?", source, suggestion)
	}
	return fmt.Sprintf("No matching package found for %s", source)
}

func findSuggestedConfiguredSource(source string, pkgs []ConfiguredPackage) string {
	trimmed := strings.TrimSpace(source)
	for _, pkg := range pkgs {
		sourceStr := pkg.Source.Source
		if name, spec, ok := parseNpmSource(sourceStr); ok {
			if trimmed == name || trimmed == spec {
				return sourceStr
			}
			continue
		}
		if git, ok := ParseGitPackageSource(sourceStr); ok {
			shorthand := git.Host + "/" + git.Path
			if trimmed == shorthand {
				return sourceStr
			}
			if git.Ref != "" && trimmed == shorthand+"@"+git.Ref {
				return sourceStr
			}
		}
	}
	return ""
}

func parseNpmSource(source string) (name, spec string, ok bool) {
	if !strings.HasPrefix(source, "npm:") {
		return "", "", false
	}
	spec = strings.TrimSpace(strings.TrimPrefix(source, "npm:"))
	name, _ = parseNpmSpec(spec)
	return name, spec, name != ""
}

func ParseGitPackageSource(source string) (GitPackageSource, bool) {
	ref, err := sourceref.Parse(source, sourceref.Options{Bare: sourceref.BareReject})
	if err != nil || ref.Kind != sourceref.KindGit {
		return GitPackageSource{}, false
	}
	return GitPackageSource{
		Host: ref.GitHost, Path: ref.GitPath, Ref: ref.GitRef, Pinned: ref.GitRef != "",
	}, true
}
