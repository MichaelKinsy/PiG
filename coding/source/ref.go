// Package source parses the source-reference vocabulary shared by package
// installation and Pig piglet resource origins. It preserves upstream Pi's
// npm/git/local grammar while allowing Pig products to contribute explicit
// lowercase schemes without teaching generic code their transport semantics.
//
// pig additive (D18): product-neutral typed source and contributed schemes.
package source

import (
	"fmt"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Kind is the transport-independent source category.
type Kind string

const (
	KindLocal       Kind = "local"
	KindNPM         Kind = "npm"
	KindGit         Kind = "git"
	KindContributed Kind = "contributed"
)

// BarePolicy controls how a source without an explicit scheme/path marker is
// interpreted at a boundary. Package install uses BareNPM for upstream parity;
// piglets require explicit typed strings.
type BarePolicy uint8

const (
	BareReject BarePolicy = iota
	BareLocal
	BareNPM
)

// Options defines boundary-specific parsing behavior.
type Options struct {
	BaseDir          string
	Bare             BarePolicy
	AllowContributed bool
}

// Ref is a parsed source. Raw is retained for settings/piglet serialization;
// identity fields exclude mutable version/ref selectors where upstream package
// de-duplication does.
type Ref struct {
	Raw     string
	Kind    Kind
	Scheme  string
	Locator string

	NPMName     string
	NPMVer      string
	NPMRegistry string

	GitHost   string
	GitPath   string
	GitRef    string
	GitRepo   string
	GitSubdir string
}

var (
	schemePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	npmSpecPattern = regexp.MustCompile(`^(@?[^@\s]+(?:/[^@\s]+)?)(?:@([^\s]+))?$`)
	windowsPath    = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// Parse validates and classifies input without materializing it.
func Parse(input string, opts Options) (Ref, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return Ref{}, fmt.Errorf("source is required")
	}

	if isExplicitLocal(raw) {
		return Ref{Raw: raw, Kind: KindLocal, Locator: raw}, nil
	}
	if after, ok := strings.CutPrefix(raw, "local:"); ok {
		locator := strings.TrimSpace(after)
		if locator == "" {
			return Ref{}, fmt.Errorf("local source locator is required")
		}
		// A local locator is a filesystem path, which may contain spaces.
		return Ref{Raw: raw, Kind: KindLocal, Scheme: "local", Locator: locator}, nil
	}
	if after, ok := strings.CutPrefix(raw, "npm:"); ok {
		return parseNPM(raw, strings.TrimSpace(after))
	}
	if isGitInput(raw) {
		return parseGit(raw)
	}

	if scheme, locator, ok := strings.Cut(raw, ":"); ok {
		if !opts.AllowContributed {
			return Ref{}, fmt.Errorf("unsupported source scheme %q", scheme)
		}
		if !schemePattern.MatchString(scheme) {
			return Ref{}, fmt.Errorf("invalid source scheme %q: must be lowercase alphanumeric with hyphens", scheme)
		}
		locator = strings.TrimSpace(locator)
		if locator == "" {
			return Ref{}, fmt.Errorf("%s source locator is required", scheme)
		}
		if strings.ContainsAny(locator, " \t\r\n") {
			return Ref{}, fmt.Errorf("%s source locator must not contain whitespace", scheme)
		}
		return Ref{Raw: raw, Kind: KindContributed, Scheme: scheme, Locator: locator}, nil
	}

	switch opts.Bare {
	case BareLocal:
		return Ref{Raw: raw, Kind: KindLocal, Locator: raw}, nil
	case BareNPM:
		return parseNPM(raw, raw)
	default:
		return Ref{}, fmt.Errorf("source %q must use an explicit scheme or path", raw)
	}
}

// Identity returns the stable de-duplication identity. npm versions and Git refs
// are deliberately excluded to match upstream package identity semantics.
func (r Ref) Identity(baseDir string) (string, error) {
	switch r.Kind {
	case KindNPM:
		identity := "npm:" + r.NPMName
		if r.NPMRegistry != "" {
			identity += "?registry=" + url.QueryEscape(r.NPMRegistry)
		}
		return identity, nil
	case KindGit:
		identity := "git:" + r.GitHost + "/" + r.GitPath
		if r.GitSubdir != "" {
			identity += "#subdirectory=" + filepath.ToSlash(r.GitSubdir)
		}
		return identity, nil
	case KindContributed:
		return r.Scheme + ":" + r.Locator, nil
	case KindLocal:
		path := r.Locator
		if rest, ok := homeRelative(path); ok {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve source home: %w", err)
			}
			path = filepath.Join(home, rest)
		}
		if windowsPath.MatchString(path) {
			return "local:" + filepath.Clean(path), nil
		}
		if !filepath.IsAbs(path) {
			if baseDir == "" {
				return "", fmt.Errorf("base directory is required for relative source %q", path)
			}
			path = filepath.Join(baseDir, path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve local source %q: %w", r.Locator, err)
		}
		return "local:" + filepath.Clean(abs), nil
	default:
		return "", fmt.Errorf("unsupported source kind %q", r.Kind)
	}
}

func isExplicitLocal(source string) bool {
	if filepath.IsAbs(source) || windowsPath.MatchString(source) || source == "." || source == ".." ||
		strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "~/") {
		return true
	}
	// Where '\' is a separator, Pi resolves .\, ..\ and ~\ as paths (paths.ts
	// normalizePath) and its config selector writes relative sources this way.
	return filepath.Separator == '\\' &&
		(strings.HasPrefix(source, `.\`) || strings.HasPrefix(source, `..\`) || strings.HasPrefix(source, `~\`))
}

// homeRelative returns the part of a ~/ (or, where '\' is a separator, ~\)
// path below the home directory.
func homeRelative(path string) (string, bool) {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return rest, true
	}
	if filepath.Separator == '\\' {
		return strings.CutPrefix(path, `~\`)
	}
	return "", false
}

func parseNPM(raw, input string) (Ref, error) {
	spec, registry, err := splitNPMRegistry(input)
	if err != nil {
		return Ref{}, fmt.Errorf("invalid npm source %q: %w", raw, err)
	}
	match := npmSpecPattern.FindStringSubmatch(spec)
	if len(match) == 0 || match[1] == "" {
		return Ref{}, fmt.Errorf("invalid npm source %q", raw)
	}
	return Ref{Raw: raw, Kind: KindNPM, Locator: spec, NPMName: match[1], NPMVer: match[2], NPMRegistry: registry}, nil
}

func splitNPMRegistry(input string) (string, string, error) {
	spec, query, hasQuery := strings.Cut(input, "?")
	if !hasQuery {
		return input, "", nil
	}
	values, err := url.ParseQuery(query)
	if err != nil || len(values) != 1 || len(values["registry"]) != 1 {
		return "", "", fmt.Errorf("query must be registry=<https-url>")
	}
	rawRegistry := strings.TrimSpace(values.Get("registry"))
	registryURL, err := url.Parse(rawRegistry)
	if err != nil || registryURL.Scheme != "https" || registryURL.Host == "" || registryURL.User != nil || registryURL.RawQuery != "" || registryURL.Fragment != "" {
		return "", "", fmt.Errorf("registry must be an absolute HTTPS URL without credentials, query, or fragment")
	}
	registryURL.Path = strings.TrimRight(registryURL.Path, "/")
	return spec, registryURL.String(), nil
}

func isGitInput(source string) bool {
	if strings.HasPrefix(source, "git:") {
		return true
	}
	lower := strings.ToLower(source)
	for _, prefix := range []string{"http://", "https://", "ssh://", "git://"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func parseGit(raw string) (Ref, error) {
	locator := raw
	if after, ok := strings.CutPrefix(raw, "git:"); ok {
		locator = strings.TrimSpace(after)
	}
	if locator == "" {
		return Ref{}, fmt.Errorf("git source locator is required")
	}
	repoLocator, subdir, err := splitGitSubdirectory(locator)
	if err != nil {
		return Ref{}, fmt.Errorf("invalid Git source %q: %w", raw, err)
	}
	repo, ref := splitGitRef(repoLocator)
	if strings.HasPrefix(repo, "git@") {
		host, repoPath, ok := splitSCPRepo(repo)
		if !ok {
			return Ref{}, fmt.Errorf("invalid Git SSH source %q", raw)
		}
		return gitRef(raw, locator, repo, host, repoPath, ref, subdir)
	}
	if strings.Contains(repo, "://") {
		parsed, err := url.Parse(repo)
		if err != nil || parsed.Hostname() == "" {
			return Ref{}, fmt.Errorf("invalid Git URL source %q", raw)
		}
		return gitRef(raw, locator, repo, parsed.Hostname(), strings.TrimPrefix(parsed.Path, "/"), ref, subdir)
	}
	host, repoPath, ok := strings.Cut(repo, "/")
	if !ok || host == "" || (!strings.Contains(host, ".") && host != "localhost") {
		return Ref{}, fmt.Errorf("invalid Git shorthand source %q", raw)
	}
	return gitRef(raw, locator, "https://"+repo, host, repoPath, ref, subdir)
}

func splitGitSubdirectory(locator string) (string, string, error) {
	repo, fragment, hasFragment := strings.Cut(locator, "#")
	if !hasFragment {
		return locator, "", nil
	}
	if strings.Contains(fragment, "#") {
		return "", "", fmt.Errorf("source has more than one fragment separator")
	}
	values, err := url.ParseQuery(fragment)
	if err != nil || len(values) != 1 || len(values["subdirectory"]) != 1 {
		return "", "", fmt.Errorf("fragment must be subdirectory=<path>")
	}
	raw := values.Get("subdirectory")
	if raw == "" || strings.Contains(raw, `\`) {
		return "", "", fmt.Errorf("subdirectory must be a non-empty portable relative path")
	}
	clean := pathpkg.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) {
		return "", "", fmt.Errorf("subdirectory %q must stay inside the Git repository", raw)
	}
	return repo, clean, nil
}

func gitRef(raw, locator, repo, host, repoPath, ref, subdir string) (Ref, error) {
	if strings.HasPrefix(repoPath, "/") {
		return Ref{}, fmt.Errorf("Git source %q has an unsafe install path", raw)
	}
	repoPath = strings.TrimSuffix(repoPath, ".git")
	if repoPath == "" || len(strings.Split(repoPath, "/")) < 2 ||
		hasUnsafeGitInstallPart(host, false) || hasUnsafeGitInstallPart(repoPath, true) {
		return Ref{}, fmt.Errorf("Git source %q must include a safe owner/repository path", raw)
	}
	return Ref{
		Raw: raw, Kind: KindGit, Scheme: "git", Locator: locator,
		GitHost: host, GitPath: repoPath, GitRef: ref, GitRepo: repo, GitSubdir: subdir,
	}, nil
}

func hasUnsafeGitInstallPart(value string, allowSlash bool) bool {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return true
	}
	for _, candidate := range []string{value, decoded} {
		if strings.ContainsAny(candidate, "\x00\\") || strings.HasPrefix(candidate, "/") ||
			!allowSlash && strings.Contains(candidate, "/") {
			return true
		}
		if slices.Contains(strings.Split(candidate, "/"), "..") {
			return true
		}
	}
	return false
}

func splitSCPRepo(repo string) (host, path string, ok bool) {
	rest, ok := strings.CutPrefix(repo, "git@")
	if !ok {
		return "", "", false
	}
	host, path, ok = strings.Cut(rest, ":")
	return host, path, ok && host != "" && path != ""
}

func splitGitRef(source string) (repo, ref string) {
	if strings.HasPrefix(source, "git@") {
		if host, path, ok := splitSCPRepo(source); ok {
			if idx := strings.Index(path, "@"); idx >= 0 && idx < len(path)-1 {
				return "git@" + host + ":" + path[:idx], path[idx+1:]
			}
		}
		return source, ""
	}
	if strings.Contains(source, "://") {
		parsed, err := url.Parse(source)
		if err != nil {
			return source, ""
		}
		path := strings.TrimPrefix(parsed.Path, "/")
		if idx := strings.Index(path, "@"); idx >= 0 && idx < len(path)-1 {
			parsed.Path = "/" + path[:idx]
			return strings.TrimSuffix(parsed.String(), "/"), path[idx+1:]
		}
		return source, ""
	}
	host, path, ok := strings.Cut(source, "/")
	if !ok {
		return source, ""
	}
	if idx := strings.Index(path, "@"); idx >= 0 && idx < len(path)-1 {
		return host + "/" + path[:idx], path[idx+1:]
	}
	return source, ""
}
