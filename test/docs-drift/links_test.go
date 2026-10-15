package docsdrift

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every link in the user documentation, Markdown or an embedded HTML src/href, must lead somewhere that exists: a relative link to a file in the repository, a site link to a page of docs/site/docs, a link to a file of the project's own repository on GitHub, and any `#anchor` on those pages. Links to other sites are not fetched: a test that depends on the network is a flake, and they are not what moves with the code.

var (
	mdLink      = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	refLinkDef  = regexp.MustCompile(`(?m)^\[[^\]]+\]:\s*(\S+)`)
	siteLink    = regexp.MustCompile(`^(?:https://pi-in-go\.dev)?/docs/(?:latest/)?([a-z0-9-]+)(?:/)?(#.*)?$`)
	githubBlob  = regexp.MustCompile(`^https://github\.com/MichaelKinsy/PiG/(?:blob|tree)/(?:main|HEAD)/([^#?]+)(#.*)?$`)
	anchorSlugs = map[string]map[string]bool{}
	htmlAnchor  = regexp.MustCompile(`<a\s+(?:id|name)="([^"]+)"`)
	// htmlRef is the target of an HTML element a Markdown page embeds, such as the README banner <img src=...>.
	htmlRef = regexp.MustCompile(`<(?:img|a|source)\s[^>]*?\b(?:src|href)="([^"]+)"`)
)

// anchorsOf returns the heading anchors of a Markdown file, as the site and GitHub slug them.
func anchorsOf(rel, body string) map[string]bool {
	if a, ok := anchorSlugs[rel]; ok {
		return a
	}
	a := siteDocAnchors(body)
	for _, m := range htmlAnchor.FindAllStringSubmatch(body, -1) {
		a[strings.ToLower(m[1])] = true
	}
	anchorSlugs[rel] = a
	return a
}

func fileBody(t *testing.T, rel string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

func TestUserDocumentationLinksResolve(t *testing.T) {
	checked := 0
	for _, src := range corpus(t) {
		if strings.HasSuffix(src.Rel, "bug-ledger.md") {
			continue
		}
		var targets []string
		fenced := false
		for line := range strings.SplitSeq(src.Body, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				fenced = !fenced
				continue
			}
			if fenced {
				continue
			}
			for _, m := range mdLink.FindAllStringSubmatch(line, -1) {
				targets = append(targets, m[1])
			}
			for _, m := range refLinkDef.FindAllStringSubmatch(line, -1) {
				targets = append(targets, m[1])
			}
			for _, m := range htmlRef.FindAllStringSubmatch(line, -1) {
				targets = append(targets, m[1])
			}
		}
		for _, target := range targets {
			checked++
			if problem := linkProblem(t, src, target); problem != "" {
				t.Errorf("%s: link %q: %s", src.Rel, target, problem)
			}
		}
	}
	if checked < 200 {
		t.Fatalf("checked only %d links; the extractor is broken", checked)
	}
}

func linkProblem(t *testing.T, src docSource, target string) string {
	t.Helper()
	target = strings.Trim(target, "<>")
	switch {
	case strings.HasPrefix(target, "mailto:"), strings.HasPrefix(target, "tel:"):
		return ""
	}
	if m := siteLink.FindStringSubmatch(target); m != nil {
		rel := "docs/site/docs/" + m[1] + ".md"
		if m[1] == "latest" {
			rel = "docs/site/docs/index.md" // /docs/latest is the site's documentation root
		}
		body, ok := fileBody(t, rel)
		if !ok {
			return "no page " + rel
		}
		return anchorProblem(rel, body, m[2])
	}
	if m := githubBlob.FindStringSubmatch(target); m != nil {
		rel := strings.TrimSuffix(m[1], "/")
		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel))); err != nil {
			return "the repository has no " + rel
		}
		if body, ok := fileBody(t, rel); ok && strings.HasSuffix(rel, ".md") {
			return anchorProblem(rel, body, m[2])
		}
		return ""
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return ""
	}
	pathPart, anchor, _ := strings.Cut(target, "#")
	if anchor != "" {
		anchor = "#" + anchor
	}
	if pathPart == "" {
		return anchorProblem(src.Rel, src.Body, anchor)
	}
	rel := path.Clean(path.Join(path.Dir(src.Rel), pathPart))
	if strings.HasPrefix(pathPart, "/") {
		return "" // a site-absolute path that is not a docs page, such as /install.sh
	}
	info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		return "the repository has no " + rel
	}
	if body, ok := fileBody(t, rel); ok && !info.IsDir() && strings.HasSuffix(rel, ".md") {
		return anchorProblem(rel, body, anchor)
	}
	return ""
}

func anchorProblem(rel, body, anchor string) string {
	anchor = strings.TrimPrefix(anchor, "#")
	if anchor == "" {
		return ""
	}
	if !anchorsOf(rel, body)[strings.ToLower(anchor)] {
		return "no heading " + anchor + " in " + rel
	}
	return ""
}
