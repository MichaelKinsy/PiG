package source

// A port of the part of hosted-git-info 9.0.3 (lib/from-url.js, lib/parse-url.js, lib/hosts.js) that packages/coding-agent/src/utils/git.ts reads:
// fromUrl's domain, user, project and committish for github, bitbucket, gitlab, gist and sourcehut sources.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// hostedInfo is hosted-git-info's GitHost as parseGitUrl reads it. A nil user is JavaScript's null.
type hostedInfo struct {
	domain     string
	user       *string
	project    string
	committish string
}

type hostedKind struct {
	domain    string
	protocols []string
	extract   func(u nodeurl.URL) (user *string, project, committish string, ok bool)
}

var hostedKinds = map[string]hostedKind{
	"github":    {"github.com", []string{"git:", "http:", "git+ssh:", "git+https:", "ssh:", "https:"}, extractGithub},
	"bitbucket": {"bitbucket.org", []string{"git+ssh:", "git+https:", "ssh:", "https:"}, extractBitbucket},
	"gitlab":    {"gitlab.com", []string{"git+ssh:", "git+https:", "ssh:", "https:"}, extractGitlab},
	"gist":      {"gist.github.com", []string{"git:", "git+ssh:", "git+https:", "ssh:", "https:"}, extractGist},
	"sourcehut": {"git.sr.ht", []string{"git+ssh:", "https:"}, extractSourcehut},
}

// hostedProtocols is hosted-git-info's protocol table: the schemes correctProtocol leaves alone.
var hostedProtocols = map[string]bool{
	"git+ssh:": true, "ssh:": true, "git+https:": true, "git:": true, "http:": true, "https:": true, "git+http:": true,
	"github:": true, "bitbucket:": true, "gitlab:": true, "gist:": true, "sourcehut:": true,
}

// hostedGitFromURL is GitHost.fromUrl(giturl): nil when the URL is not a hosted source.
func hostedGitFromURL(giturl string) *hostedInfo {
	if giturl == "" {
		return nil
	}
	corrected := giturl
	if isGitHubShorthand(giturl) {
		corrected = "github:" + giturl
	}
	parsed, ok := parseHostedURL(corrected)
	if !ok {
		return nil
	}
	name := ""
	if shortcut, found := strings.CutSuffix(parsed.Protocol, ":"); found {
		if _, isHost := hostedKinds[shortcut]; isHost {
			name = shortcut
		}
	}
	shortcut := name != ""
	if !shortcut {
		hostname := strings.TrimPrefix(parsed.Hostname, "www.")
		for candidate, kind := range hostedKinds {
			if kind.domain == hostname {
				name = candidate
			}
		}
	}
	if name == "" {
		return nil
	}
	kind := hostedKinds[name]
	info := &hostedInfo{domain: kind.domain}
	if shortcut {
		pathname := strings.TrimPrefix(parsed.Pathname, "/")
		if at := strings.Index(pathname, "@"); at > -1 {
			pathname = pathname[at+1:]
		}
		projectText := pathname
		if lastSlash := strings.LastIndex(pathname, "/"); lastSlash > -1 {
			user, err := nodeurl.DecodeURIComponent(pathname[:lastSlash])
			if err != nil {
				return nil
			}
			if user != "" {
				info.user = &user
			}
			projectText = pathname[lastSlash+1:]
		}
		project, err := nodeurl.DecodeURIComponent(projectText)
		if err != nil {
			return nil
		}
		info.project = strings.TrimSuffix(project, ".git")
		if parsed.Hash != "" {
			committish, err := nodeurl.DecodeURIComponent(parsed.Hash[1:])
			if err != nil {
				return nil
			}
			info.committish = committish
		}
		return info
	}
	allowed := false
	for _, protocol := range kind.protocols {
		allowed = allowed || protocol == parsed.Protocol
	}
	if !allowed {
		return nil
	}
	user, project, committish, ok := kind.extract(parsed)
	if !ok {
		return nil
	}
	if user != nil {
		decoded, err := nodeurl.DecodeURIComponent(*user)
		if err != nil {
			return nil
		}
		info.user = &decoded
	}
	var err error
	if info.project, err = nodeurl.DecodeURIComponent(project); err != nil {
		return nil
	}
	if info.committish, err = nodeurl.DecodeURIComponent(committish); err != nil {
		return nil
	}
	return info
}

// jsSplit is String.prototype.split(sep, limit): the first limit pieces of the full split.
func jsSplit(s, sep string, limit int) []string {
	pieces := strings.Split(s, sep)
	if len(pieces) > limit {
		pieces = pieces[:limit]
	}
	return pieces
}

func piece(pieces []string, i int) string {
	if i < len(pieces) {
		return pieces[i]
	}
	return ""
}

func extractGithub(u nodeurl.URL) (*string, string, string, bool) {
	pieces := jsSplit(u.Pathname, "/", 5)
	user, project, kind := piece(pieces, 1), piece(pieces, 2), piece(pieces, 3)
	committish := piece(pieces, 4)
	if kind != "" && kind != "tree" {
		return nil, "", "", false
	}
	if kind == "" {
		committish = strings.TrimPrefix(u.Hash, "#")
	} else if len(pieces) < 5 {
		// decodeURIComponent(undefined) is the string "undefined".
		committish = "undefined"
	}
	project = strings.TrimSuffix(project, ".git")
	if user == "" || project == "" {
		return nil, "", "", false
	}
	return &user, project, committish, true
}

func extractBitbucket(u nodeurl.URL) (*string, string, string, bool) {
	pieces := jsSplit(u.Pathname, "/", 4)
	user, project := piece(pieces, 1), piece(pieces, 2)
	if piece(pieces, 3) == "get" {
		return nil, "", "", false
	}
	project = strings.TrimSuffix(project, ".git")
	if user == "" || project == "" {
		return nil, "", "", false
	}
	return &user, project, strings.TrimPrefix(u.Hash, "#"), true
}

func extractGitlab(u nodeurl.URL) (*string, string, string, bool) {
	path := strings.TrimPrefix(u.Pathname, "/")
	if strings.Contains(path, "/-/") || strings.Contains(path, "/archive.tar.gz") {
		return nil, "", "", false
	}
	segments := strings.Split(path, "/")
	project := strings.TrimSuffix(segments[len(segments)-1], ".git")
	user := strings.Join(segments[:len(segments)-1], "/")
	if user == "" || project == "" {
		return nil, "", "", false
	}
	return &user, project, strings.TrimPrefix(u.Hash, "#"), true
}

func extractGist(u nodeurl.URL) (*string, string, string, bool) {
	pieces := jsSplit(u.Pathname, "/", 4)
	user, project := piece(pieces, 1), piece(pieces, 2)
	if piece(pieces, 3) == "raw" {
		return nil, "", "", false
	}
	var userRef *string
	if project == "" {
		if user == "" {
			return nil, "", "", false
		}
		project = user
	} else {
		userRef = &user
	}
	project = strings.TrimSuffix(project, ".git")
	return userRef, project, strings.TrimPrefix(u.Hash, "#"), true
}

func extractSourcehut(u nodeurl.URL) (*string, string, string, bool) {
	pieces := jsSplit(u.Pathname, "/", 4)
	user, project := piece(pieces, 1), piece(pieces, 2)
	if piece(pieces, 3) == "archive" {
		return nil, "", "", false
	}
	project = strings.TrimSuffix(project, ".git")
	if user == "" || project == "" {
		return nil, "", "", false
	}
	return &user, project, strings.TrimPrefix(u.Hash, "#"), true
}

var jsWhitespace = lazyregexp.New(`[\s\x{a0}\x{feff}\x{2028}\x{2029}\x{1680}\x{2000}-\x{200a}\x{202f}\x{205f}\x{3000}]`)

// isGitHubShorthand is from-url.js isGitHubShorthand: an owner/repository such as npm/cli.
func isGitHubShorthand(arg string) bool {
	firstHash := strings.Index(arg, "#")
	firstSlash := strings.Index(arg, "/")
	secondSlash := -1
	if firstSlash >= 0 {
		if next := strings.Index(arg[firstSlash+1:], "/"); next >= 0 {
			secondSlash = firstSlash + 1 + next
		}
	}
	firstColon := strings.Index(arg, ":")
	firstSpace := -1
	if loc := jsWhitespace.FindStringIndex(arg); loc != nil {
		firstSpace = loc[0]
	}
	firstAt := strings.Index(arg, "@")
	spaceOnlyAfterHash := firstSpace < 0 || (firstHash > -1 && firstSpace > firstHash)
	atOnlyAfterHash := firstAt == -1 || (firstHash > -1 && firstAt > firstHash)
	colonOnlyAfterHash := firstColon == -1 || (firstHash > -1 && firstColon > firstHash)
	secondSlashOnlyAfterHash := secondSlash == -1 || (firstHash > -1 && secondSlash > firstHash)
	hasSlash := firstSlash > 0
	doesNotEndWithSlash := !strings.HasSuffix(arg, "/")
	if firstHash > -1 {
		doesNotEndWithSlash = arg[firstHash-1] != '/'
	}
	return spaceOnlyAfterHash && hasSlash && doesNotEndWithSlash && !strings.HasPrefix(arg, ".") && atOnlyAfterHash && colonOnlyAfterHash && secondSlashOnlyAfterHash
}

// parseHostedURL is parse-url.js: new URL(correctProtocol(arg)) or, failing that, new URL(correctUrl(...)).
func parseHostedURL(arg string) (nodeurl.URL, bool) {
	withProtocol := correctProtocol(arg)
	if parsed, err := nodeurl.ParseURL(withProtocol); err == nil {
		return parsed, true
	}
	parsed, err := nodeurl.ParseURL(correctURL(withProtocol))
	return parsed, err == nil
}

// correctProtocol turns git:github.com:user/repo into a URL by inserting the // after the first colon.
func correctProtocol(arg string) string {
	firstColon := strings.Index(arg, ":")
	if hostedProtocols[arg[:firstColon+1]] {
		return arg
	}
	if firstColon >= 0 && strings.HasPrefix(arg[firstColon:], "://") {
		return arg
	}
	if firstAt := strings.Index(arg, "@"); firstAt > -1 {
		if firstAt > firstColon {
			return "git+ssh://" + arg
		}
		return arg
	}
	return arg[:firstColon+1] + "//" + arg[firstColon+1:]
}

// lastIndexBefore is parse-url.js lastIndexOfBefore: the last char at or before the first beforeChar.
func lastIndexBefore(s string, char, beforeChar byte) int {
	start := strings.IndexByte(s, beforeChar)
	if start < 0 {
		start = len(s) - 1
	}
	for i := min(start, len(s)-1); i >= 0; i-- {
		if s[i] == char {
			return i
		}
	}
	return -1
}

// correctURL rewrites an scp-style URL so that it parses as a URL.
func correctURL(giturl string) string {
	firstAt := lastIndexBefore(giturl, '@', '#')
	lastColonBeforeHash := lastIndexBefore(giturl, ':', '#')
	if lastColonBeforeHash > firstAt {
		giturl = giturl[:lastColonBeforeHash] + "/" + giturl[lastColonBeforeHash+1:]
	}
	if lastIndexBefore(giturl, ':', '#') == -1 && !strings.Contains(giturl, "//") {
		giturl = "git+ssh://" + giturl
	}
	return giturl
}
