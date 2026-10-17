package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// getLatestVersion's redirect parsing (utils/tools-manager.ts:133-139) is not exported by Pi's module, so the oracle body transcribes it and runs it on Node's own URL and decodeURIComponent: the Location header is resolved against https://github.com, its last path segment is the tag.
func TestLatestReleaseTagVersionMatchesNodeURL(t *testing.T) {
	locations := []string{
		"https://github.com/sharkdp/fd/releases/tag/v10.2.0", "https://github.com/sharkdp/fd/releases/tag/10.2.0", "/sharkdp/fd/releases/tag/v10.2.0", "sharkdp/fd/releases/tag/v1", "//github.com/a/b/releases/tag/v2", "https://github.com/a/b/releases/tag/v1/",
		"https://github.com/a/b/releases/tag/v1?x=1#f", "https://github.com/a/b/releases/tag/v%31.0", "https://github.com/a/b/releases/tag/%E2%9C%93", "https://github.com/a/b/releases/tag/%zz", "https://github.com/a/b/releases/tag/%E2%9C", "https://github.com/a/b/releases/tag/v1%2F2",
		"https://github.com/a/b/releases/tag/vv1", "https://github.com/a/b/releases/tag/v", "https://github.com/a/b/releases/tag/", "https://github.com/a/b/releases/latest", "https://github.com/a/b/releases/tag/../tag/v3", "https://github.com/a/b/releases/tag/./v3", "https://github.com/a/b/releases/tag/%2e%2e/v3",
		"https://github.com\\a\\b\\releases\\tag\\v1", "https://github.com/a/b/releases/tag/v 1", "https://github.com/a/b/releases/tag/é", " https://github.com/a/b/releases/tag/v1 ", "https://GITHUB.com:443/a/b/releases/tag/v1", "https://github.com/a/b/releases/tag/v1\t", "http://github.com/a/b/releases/tag/v4",
		"ftp://github.com/a/b/releases/tag/v5", "custom:/a/b/releases/tag/v6", "custom://h/a/b/releases/tag/v6", "mailto:a/releases/tag/v7", "https://", "https://[::1", "", "?x=/releases/tag/v1", "#/releases/tag/v1", "../releases/tag/v8", "./releases/tag/v8", "releases/tag/v9", "\\releases\\tag\\v1", "/releases/tag/v1\\v2",
	}
	var want []struct {
		Version string `json:"version"`
		Error   bool   `json:"error"`
	}
	pioracle.Run(t, `
emit(input.map((location) => {
	try {
		const tag = new URL(location, "https://github.com").pathname.split("/").pop();
		if (!tag || !location.includes("/releases/tag/")) return { error: true };
		return { version: decodeURIComponent(tag).replace(/^v/, "") };
	} catch { return { error: true }; }
}));`, locations, &want)
	for i, location := range locations {
		version, err := latestReleaseTagVersion("o/r", location)
		if (err != nil) != want[i].Error || (err == nil && version != want[i].Version) {
			t.Errorf("location=%q: Go %q err=%v, Pi %+v", location, version, err, want[i])
		}
	}
}
