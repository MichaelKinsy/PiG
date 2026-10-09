package source

// Ports packages/coding-agent/src/utils/git.ts parseGitUrl: Git sources of every historical spelling, with hosted-git-info's lookup for the hosted
// forms (hostedgit.go).

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// gitSource is Pi's GitSource without its constant type and the derived pinned flag.
type gitSource struct {
	repo, host, path, ref string
}

var (
	scpLikePattern    = lazyregexp.New(`^git@([^:]+):(.+)$`)
	protocolURLPrefix = lazyregexp.New(`(?i)^(https?|ssh|git)://`)
)

// parseRepoURL is `new URL(repository)` for a repository URL. pig divergence (D110): a file:// repository (a local bare repository, which Piglet
// installs and tests use) is read as a non-special URL so its host, such as localhost, stays; Node's file URL would empty it.
func parseRepoURL(raw string) (nodeurl.URL, error) {
	if len(raw) >= 5 && strings.EqualFold(raw[:5], "file:") {
		parsed, err := nodeurl.ParseURL("pig-file:" + raw[5:])
		parsed.Protocol = "file:"
		return parsed, err
	}
	return nodeurl.ParseURL(raw)
}

// splitGitRef is splitRef: the repository and the ref after the first "@" of its path.
func splitGitRef(source string) (repo, ref string) {
	if match := scpLikePattern.FindStringSubmatch(source); match != nil {
		pathWithMaybeRef := match[2]
		separator := strings.Index(pathWithMaybeRef, "@")
		if separator < 0 {
			return source, ""
		}
		repoPath, ref := pathWithMaybeRef[:separator], pathWithMaybeRef[separator+1:]
		if repoPath == "" || ref == "" {
			return source, ""
		}
		return "git@" + match[1] + ":" + repoPath, ref
	}
	if strings.Contains(source, "://") {
		parsed, err := parseRepoURL(source)
		if err != nil {
			return source, ""
		}
		pathWithMaybeRef := strings.TrimLeft(parsed.Pathname, "/")
		separator := strings.Index(pathWithMaybeRef, "@")
		if separator < 0 {
			return source, ""
		}
		repoPath, ref := pathWithMaybeRef[:separator], pathWithMaybeRef[separator+1:]
		if repoPath == "" || ref == "" {
			return source, ""
		}
		return strings.TrimSuffix(parsed.WithPathname("/"+repoPath), "/"), ref
	}
	slash := strings.Index(source, "/")
	if slash < 0 {
		return source, ""
	}
	host, pathWithMaybeRef := source[:slash], source[slash+1:]
	separator := strings.Index(pathWithMaybeRef, "@")
	if separator < 0 {
		return source, ""
	}
	repoPath, ref := pathWithMaybeRef[:separator], pathWithMaybeRef[separator+1:]
	if repoPath == "" || ref == "" {
		return source, ""
	}
	return host + "/" + repoPath, ref
}

// hasUnsafeGitInstallPart is hasUnsafeGitInstallPart in git.ts: a host or path part that could leave the Git install root once decoded.
func hasUnsafeGitInstallPart(value string, allowSlash bool) bool {
	decoded, err := nodeurl.DecodeURIComponent(value)
	if err != nil {
		return true
	}
	for _, candidate := range []string{value, decoded} {
		if strings.ContainsAny(candidate, "\x00\\") || strings.HasPrefix(candidate, "/") || !allowSlash && strings.Contains(candidate, "/") {
			return true
		}
		for _, part := range strings.Split(candidate, "/") {
			if part == ".." {
				return true
			}
		}
	}
	return false
}

// buildGitSource is buildGitSource in git.ts: nil unless the path is a safe owner/repository below a host.
func buildGitSource(repo, host, path, ref string) *gitSource {
	if strings.HasPrefix(path, "/") {
		return nil
	}
	normalized := strings.TrimLeft(strings.TrimSuffix(path, ".git"), "/")
	if host == "" || normalized == "" || len(strings.Split(normalized, "/")) < 2 {
		return nil
	}
	if hasUnsafeGitInstallPart(host, false) || hasUnsafeGitInstallPart(normalized, true) {
		return nil
	}
	return &gitSource{repo: repo, host: host, path: normalized, ref: ref}
}

var scpLikeRepoPattern = lazyregexp.New(`^git@([^:]+):(.+)$`)

// parseGenericGitURL is parseGenericGitUrl: an scp-like, protocol or host/path source with no hosted-git-info match.
func parseGenericGitURL(rawURL string) *gitSource {
	repoWithoutRef, ref := splitGitRef(rawURL)
	repo, host, path := repoWithoutRef, "", ""
	if match := scpLikeRepoPattern.FindStringSubmatch(repoWithoutRef); match != nil {
		host, path = match[1], match[2]
	} else if strings.HasPrefix(repoWithoutRef, "https://") || strings.HasPrefix(repoWithoutRef, "http://") || strings.HasPrefix(repoWithoutRef, "ssh://") || strings.HasPrefix(repoWithoutRef, "git://") || strings.HasPrefix(repoWithoutRef, "file://") {
		parsed, err := parseRepoURL(repoWithoutRef)
		if err != nil {
			return nil
		}
		host, path = parsed.Hostname, strings.TrimLeft(parsed.Pathname, "/")
	} else {
		var found bool
		host, path, found = strings.Cut(repoWithoutRef, "/")
		if !found {
			return nil
		}
		if !strings.Contains(host, ".") && host != "localhost" {
			return nil
		}
		repo = "https://" + repoWithoutRef
	}
	return buildGitSource(repo, host, path, ref)
}

// parseGitURL is parseGitUrl. Without the git: prefix only explicit protocol URLs are accepted.
//
// pig divergence (D110): a source starting with "git://" is a protocol URL, not the git: prefix followed by "//" (Pi gives the unusable repository
// "https:////host/..."), and the scheme of a protocol URL is matched without regard to case when deciding whether to prefix https:// (Pi gives
// "https://HTTPS://host/...").
func parseGitURL(source string) *gitSource {
	trimmed := resolvepath.Trim(source)
	hasGitPrefix := strings.HasPrefix(trimmed, "git:") && !strings.HasPrefix(strings.ToLower(trimmed), "git://")
	rawURL := trimmed
	if hasGitPrefix {
		rawURL = resolvepath.Trim(trimmed[4:])
	}
	if !hasGitPrefix && !protocolURLPrefix.MatchString(rawURL) {
		return nil
	}
	repo, ref := splitGitRef(rawURL)
	var hostedCandidates []string
	if ref != "" {
		hostedCandidates = append(hostedCandidates, repo+"#"+ref)
	}
	if rawURL != "" {
		hostedCandidates = append(hostedCandidates, rawURL)
	}
	for _, candidate := range hostedCandidates {
		info := hostedGitFromURL(candidate)
		if info == nil {
			continue
		}
		if ref != "" && strings.Contains(info.project, "@") {
			continue
		}
		lowerRepo := strings.ToLower(repo)
		useHTTPSPrefix := !strings.HasPrefix(lowerRepo, "http://") && !strings.HasPrefix(lowerRepo, "https://") && !strings.HasPrefix(lowerRepo, "ssh://") &&
			!strings.HasPrefix(lowerRepo, "git://") && !strings.HasPrefix(repo, "git@")
		resolved := repo
		if useHTTPSPrefix {
			resolved = "https://" + repo
		}
		user := "null"
		if info.user != nil {
			user = *info.user
		}
		committish := info.committish
		if committish == "" {
			committish = ref
		}
		return buildGitSource(resolved, info.domain, user+"/"+info.project, committish)
	}
	var httpsCandidates []string
	if ref != "" {
		httpsCandidates = append(httpsCandidates, "https://"+repo+"#"+ref)
	}
	httpsCandidates = append(httpsCandidates, "https://"+rawURL)
	for _, candidate := range httpsCandidates {
		info := hostedGitFromURL(candidate)
		if info == nil {
			continue
		}
		if ref != "" && strings.Contains(info.project, "@") {
			continue
		}
		user := "null"
		if info.user != nil {
			user = *info.user
		}
		committish := info.committish
		if committish == "" {
			committish = ref
		}
		return buildGitSource("https://"+repo, info.domain, user+"/"+info.project, committish)
	}
	return parseGenericGitURL(rawURL)
}
