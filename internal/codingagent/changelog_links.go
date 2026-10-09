package codingagent

// Ports packages/coding-agent/src/utils/changelog.ts (normalizeChangelogLinks and its helpers).

import (
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

const (
	changelogGitHubRepo   = "earendil-works/pi"
	changelogLinkBasePath = "packages/coding-agent"
)

// jsSpace is the JavaScript `\s` class.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var (
	// legacyChangelogRepoRE is /^https:\/\/github\.com\/(?:badlogic|earendil-works)\/pi-mono(?=\/|$)/; the lookahead is checked by
	// changelogCanonicalRepoURL because RE2 has none.
	legacyChangelogRepoRE      = lazyregexp.New(`^https://github\.com/(?:badlogic|earendil-works)/pi-mono`)
	changelogURLSchemeRE       = lazyregexp.New(`(?i)^[a-z][a-z0-9+.-]*:`)
	inlineMarkdownLinkRE       = lazyregexp.New(`(!?\[[^\]\n]+\]\()([^` + jsSpace + `)]+)((?:[` + jsSpace + `]+[^)]*)?\))`)
	changelogPathLeadingSlashs = lazyregexp.New(`^/+`)
)

// Version is the entry's "major.minor.patch" string (entryVersion).
func (e ChangelogEntry) Version() string {
	return strconv.Itoa(e.Major) + "." + strconv.Itoa(e.Minor) + "." + strconv.Itoa(e.Patch)
}

func normalizeChangelogTag(version string) string {
	if strings.HasPrefix(version, "v") {
		return version
	}
	return "v" + version
}

// splitChangelogLocalTarget splits a link target into its path, query and fragment (splitLocalTarget).
func splitChangelogLocalTarget(target string) (fragment, pathPart, query string) {
	beforeHash := target
	if hashIndex := strings.Index(target, "#"); hashIndex != -1 {
		beforeHash, fragment = target[:hashIndex], target[hashIndex:]
	}
	if queryIndex := strings.Index(beforeHash, "?"); queryIndex != -1 {
		return fragment, beforeHash[:queryIndex], beforeHash[queryIndex:]
	}
	return fragment, beforeHash, ""
}

// resolveChangelogRepositoryPath is resolveRepositoryPath: the repository path a local link names, or false when it leaves the repository.
func resolveChangelogRepositoryPath(targetPath string) (string, bool) {
	normalized := strings.ReplaceAll(targetPath, `\`, "/")
	var joined string
	if strings.HasPrefix(normalized, "/") {
		joined = nodepath.PosixNormalize(changelogPathLeadingSlashs.ReplaceAllString(normalized, ""))
	} else {
		joined = nodepath.PosixNormalize(nodepath.PosixJoin(changelogLinkBasePath, normalized))
	}
	if joined == "." || strings.HasPrefix(joined, "../") || joined == ".." {
		return "", false
	}
	return joined, true
}

func isChangelogDirectoryTarget(originalPath, repositoryPath string) bool {
	if strings.HasSuffix(originalPath, "/") {
		return true
	}
	return !strings.Contains(nodepath.PosixBasename(repositoryPath), ".")
}

// encodeURI is JavaScript's encodeURI: every UTF-8 byte except the unreserved and reserved URI characters is percent-encoded.
func encodeURI(text string) string {
	const keep = "-_.!~*'();/?:@&=+$,#"
	var b strings.Builder
	for i := range len(text) {
		c := text[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte(keep, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		const hex = "0123456789ABCDEF"
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

func normalizeChangelogLinkTarget(target, tag string) string {
	repoURL := "https://github.com/" + changelogGitHubRepo
	canonical := target
	if loc := legacyChangelogRepoRE.FindStringIndex(target); loc != nil && (loc[1] == len(target) || target[loc[1]] == '/') {
		canonical = repoURL + target[loc[1]:]
	}
	for _, route := range []string{"blob", "tree"} {
		for _, branch := range []string{"main", "master"} {
			floatingRefPrefix := repoURL + "/" + route + "/" + branch + "/"
			if strings.HasPrefix(canonical, floatingRefPrefix) {
				canonical = repoURL + "/" + route + "/" + tag + "/" + canonical[len(floatingRefPrefix):]
			}
		}
	}
	if strings.HasPrefix(canonical, "#") || strings.HasPrefix(canonical, "//") || changelogURLSchemeRE.MatchString(canonical) {
		return canonical
	}
	fragment, pathPart, query := splitChangelogLocalTarget(canonical)
	if pathPart == "" {
		return canonical
	}
	repositoryPath, ok := resolveChangelogRepositoryPath(pathPart)
	if !ok {
		return canonical
	}
	route := "blob"
	if isChangelogDirectoryTarget(pathPart, repositoryPath) {
		route = "tree"
	}
	return repoURL + "/" + route + "/" + tag + "/" + encodeURI(repositoryPath) + query + fragment
}

// NormalizeChangelogLinks is normalizeChangelogLinks(markdown, version): inline Markdown link targets become tag-pinned GitHub source
// links, and legacy pi-mono repository URLs are canonicalized. version is "x.y.z" or "vx.y.z" (entry.Version()).
func NormalizeChangelogLinks(markdown, version string) string {
	tag := normalizeChangelogTag(version)
	return inlineMarkdownLinkRE.ReplaceAllStringFunc(markdown, func(match string) string {
		groups := inlineMarkdownLinkRE.FindStringSubmatch(match)
		return groups[1] + normalizeChangelogLinkTarget(groups[2], tag) + groups[3]
	})
}
