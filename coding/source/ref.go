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
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Kind is the transport-independent source category.
type Kind string

const (
	KindLocal       Kind = "local"
	KindNPM         Kind = "npm"
	KindGit         Kind = "git"
	KindContributed Kind = "contributed"
)

// BarePolicy controls how a source without an explicit scheme/path marker is interpreted. Each caller selects local, npm, or rejection semantics.
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
	schemePattern = lazyregexp.New(`^[a-z][a-z0-9-]*$`)
	// Selectors retain npm range whitespace; Pi's regexp dot excludes JavaScript line terminators.
	// upstream: packages/coding-agent/src/core/package-manager.ts:parseNpmSpec
	npmSpecPattern = lazyregexp.New(`^(@?[^@\s]+(?:/[^@\s]+)?)(?:@([^\r\n\x{2028}\x{2029}]+))?$`)
	windowsPath    = lazyregexp.New(`^[A-Za-z]:[\\/]`)
)

// Parse validates and classifies input without materializing it. npm selectors retain spaces and tabs used by comparator sets, hyphen ranges, and unions.
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
	// Pi package-manager.ts:1446-1470 treats bare SCP spelling as a local path, not a source scheme.
	if opts.Bare == BareLocal && strings.HasPrefix(raw, "git@") {
		return Ref{Raw: raw, Kind: KindLocal, Locator: raw}, nil
	}

	// Pi's isLocalPath (paths.ts:50-64) makes a file: source local; its path is the URL's file path.
	if strings.HasPrefix(resolvepath.Trim(raw), "file:") {
		return Ref{Raw: raw, Kind: KindLocal, Locator: raw}, nil
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
		path := resolvepath.Trim(r.Locator)
		// Explicit foreign Windows paths retain their spelling on POSIX hosts.
		if runtime.GOOS != "windows" && windowsPath.MatchString(path) {
			return "local:" + filepath.Clean(path), nil
		}
		_, homePath := homeRelative(path)
		if baseDir == "" && !filepath.IsAbs(path) && !windowsPath.MatchString(path) && path != "~" && !homePath && !strings.HasPrefix(path, "file://") {
			return "", fmt.Errorf("base directory is required for relative source %q", path)
		}
		resolved, err := resolvepath.ResolvePackagePath(path, baseDir)
		if err != nil {
			return "", fmt.Errorf("resolve local source %q: %w", r.Locator, err)
		}
		return "local:" + resolved, nil
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
	// A "git://" URL is a protocol URL, not the "git:" shorthand prefix followed by "//..." (D110).
	if after, ok := strings.CutPrefix(raw, "git:"); ok && !strings.HasPrefix(strings.ToLower(raw), "git://") {
		locator = strings.TrimSpace(after)
	}
	if locator == "" {
		return Ref{}, fmt.Errorf("git source locator is required")
	}
	// pig divergence (D110): a fragment that starts with "subdirectory=" selects a subdirectory of the repository; any other fragment is a ref, as in Pi.
	source, subdir, err := splitGitSubdirectory(raw)
	if err != nil {
		return Ref{}, fmt.Errorf("invalid Git source %q: %w", raw, err)
	}
	parsed := parseGitURL(source)
	if parsed == nil {
		return Ref{}, fmt.Errorf("invalid Git source %q: it must be a git: shorthand or a protocol URL with a safe owner/repository path", raw)
	}
	return Ref{Raw: raw, Kind: KindGit, Scheme: "git", Locator: locator, GitHost: parsed.host, GitPath: parsed.path, GitRef: parsed.ref, GitRepo: parsed.repo, GitSubdir: subdir}, nil
}

func splitGitSubdirectory(locator string) (string, string, error) {
	repo, fragment, hasFragment := strings.Cut(locator, "#")
	if !hasFragment || !strings.HasPrefix(fragment, "subdirectory=") {
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
