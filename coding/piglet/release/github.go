package release

import (
	"fmt"
	"net/url"
	"strings"
)

// GitHubRelease binds a signed index to its repository and per-Piglet tag namespace.
// An empty TagPrefix selects the repository's unprefixed v<version> tags.
type GitHubRelease struct {
	Repository string `json:"repository"`
	TagPrefix  string `json:"tagPrefix"`
}

// Tag returns the Git tag for a release version.
func (g GitHubRelease) Tag(version string) string { return g.TagPrefix + "v" + version }

// Reference returns the explicit pull reference for this namespace and version.
func (g GitHubRelease) Reference(version string) string {
	name := strings.TrimSuffix(g.TagPrefix, "/")
	repo := g.Repository
	if name != "" {
		repo += "/" + name
	}
	return "github:" + repo + "@" + version
}

// IndexURL returns the release download URL with its Git tag escaped as one segment.
func (g GitHubRelease) IndexURL(version string) string {
	return "https://github.com/" + g.Repository + "/releases/download/" + url.PathEscape(g.Tag(version)) + "/piglet-release.json"
}

// pig additive (D18): explicit name/ prefixes prevent repository layout changes from changing release identity.
func (g GitHubRelease) validate(piglet string) error {
	if !ValidGitHubRepository(g.Repository) {
		return fmt.Errorf("invalid GitHub release repository")
	}
	if g.TagPrefix != "" && (!validGitHubPart(piglet) || g.TagPrefix != piglet+"/") {
		return fmt.Errorf("GitHub release tag prefix must be empty or the Piglet name followed by /")
	}
	return nil
}

type releaseReference struct {
	url     string
	version string
	piglet  string
	github  *GitHubRelease
}

func parseReleaseReference(ref, version string) (releaseReference, error) {
	requested := strings.TrimPrefix(version, "v")
	if strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "http://") {
		if _, err := validateRemoteURL(ref); err != nil {
			return releaseReference{}, err
		}
		if requested != "" && !validReleaseVersion(requested) {
			return releaseReference{}, fmt.Errorf("invalid Piglet release version %q", version)
		}
		return releaseReference{url: ref, version: requested}, nil
	}
	raw, ok := strings.CutPrefix(ref, "github:")
	if !ok {
		return releaseReference{}, fmt.Errorf("use an HTTPS index URL or github:owner/repo[/piglet]@version")
	}
	repository, v, explicit := strings.Cut(raw, "@")
	if explicit {
		v = strings.TrimPrefix(v, "v")
	} else {
		v = requested
	}
	parts := strings.Split(repository, "/")
	if len(parts) < 2 || len(parts) > 3 || !validReleaseVersion(v) {
		return releaseReference{}, fmt.Errorf("invalid GitHub Piglet release ref; want github:owner/repo[/piglet]@version")
	}
	for _, part := range parts {
		if !validGitHubPart(part) {
			return releaseReference{}, fmt.Errorf("invalid GitHub Piglet release ref")
		}
	}
	if requested != "" && requested != v {
		return releaseReference{}, fmt.Errorf("GitHub release ref version %s does not match --version %s", v, version)
	}
	g := &GitHubRelease{Repository: parts[0] + "/" + parts[1]}
	name := ""
	if len(parts) == 3 {
		name = parts[2]
		g.TagPrefix = name + "/"
	}
	return releaseReference{url: g.IndexURL(v), version: v, piglet: name, github: g}, nil
}

func (r releaseReference) match(index Index) error {
	if r.version != "" && r.version != index.Version {
		return fmt.Errorf("Piglet release index is version %s, requested %s", index.Version, r.version)
	}
	if r.piglet != "" && r.piglet != index.Piglet {
		return fmt.Errorf("Piglet release index names %s, requested %s", index.Piglet, r.piglet)
	}
	if r.github != nil && (index.GitHub == nil || *r.github != *index.GitHub) {
		return fmt.Errorf("Piglet release index GitHub repository or tag namespace does not match requested release")
	}
	return nil
}
