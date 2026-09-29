package docsdrift

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// siteDocsDir holds the user documentation that https://pi-in-go.dev renders.
// The site's build lives in the private hosting repository and builds from
// this directory, so this repository checks the manifest rules its build
// enforces (scripts/build-docs.mjs there) on every change here.
const siteDocsDir = "../../docs/site/docs"

var siteDocHeading = regexp.MustCompile(`(?m)^#\s+(.+)$`)

// siteNavItem is a navigation entry. An entry with Items is a nested group;
// otherwise it names a page by Path. A Pending page may be absent: the site
// omits it from the rendered navigation until the page exists.
type siteNavItem struct {
	Title   string        `json:"title"`
	Path    any           `json:"path"`
	Pending bool          `json:"pending"`
	Items   []siteNavItem `json:"items"`
}

type siteRedirect struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type siteDocsManifest struct {
	Navigation []siteNavItem     `json:"navigation"`
	Provenance map[string]string `json:"provenance"`
	Redirects  []siteRedirect    `json:"redirects"`
}

func TestSiteDocsManifestClassifiesAndNavigatesEveryPage(t *testing.T) {
	manifest := readSiteDocsManifest(t)
	pages := readSiteDocPages(t)

	for name, body := range pages {
		if !siteDocHeading.MatchString(body) {
			t.Errorf("docs/site/docs/%s must contain a level-one heading", name)
		}
		switch manifest.Provenance[name] {
		case "pig-authored", "adapted-pi", "upstream-pi":
		default:
			t.Errorf("docs/site/docs/%s must have a valid provenance classification (pig-authored, adapted-pi, or upstream-pi)", name)
		}
	}

	var stale []string
	for name := range manifest.Provenance {
		if _, ok := pages[filepath.Base(name)]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("provenance references missing docs/site/docs/%s", name)
	}

	var visit func(items []siteNavItem, depth int)
	visit = func(items []siteNavItem, depth int) {
		for _, item := range items {
			if item.Items != nil {
				if depth > 1 || item.Path != nil || len(item.Items) == 0 {
					t.Errorf("navigation group %q must be a group nested one level deep with items and no path", item.Title)
				}
				visit(item.Items, depth+1)
				continue
			}
			path, ok := item.Path.(string)
			if !ok || !strings.HasSuffix(path, ".md") {
				t.Errorf("navigation item %q must name a Markdown path", item.Title)
				continue
			}
			if _, ok := pages[filepath.Base(path)]; !ok && !item.Pending {
				t.Errorf("navigation item %q references missing docs/site/docs/%s", item.Title, path)
			}
		}
	}
	for _, group := range manifest.Navigation {
		visit(group.Items, 1)
	}
}

// TestSiteDocsRedirectsResolve checks the redirect rules the site build
// enforces: a page redirect names a page that no longer exists, an anchor
// redirect names an anchor its page no longer has, and every target page and
// anchor exists.
func TestSiteDocsRedirectsResolve(t *testing.T) {
	manifest := readSiteDocsManifest(t)
	pages := readSiteDocPages(t)
	anchors := map[string]map[string]bool{}
	for name, body := range pages {
		anchors[name] = siteDocAnchors(body)
	}

	seen := map[string]bool{}
	for _, redirect := range manifest.Redirects {
		fromPage, fromAnchor, fromOK := strings.Cut(redirect.From, "#")
		toPage, toAnchor, toOK := strings.Cut(redirect.To, "#")
		if !strings.HasSuffix(fromPage, ".md") || !strings.HasSuffix(toPage, ".md") || (fromOK && fromAnchor == "") || (toOK && toAnchor == "") {
			t.Errorf("redirect %q -> %q must name Markdown pages with optional non-empty anchors", redirect.From, redirect.To)
			continue
		}
		if seen[redirect.From] {
			t.Errorf("redirect from %q is declared twice", redirect.From)
		}
		seen[redirect.From] = true
		if fromOK {
			if _, ok := pages[fromPage]; !ok {
				t.Errorf("anchor redirect %q names missing docs/site/docs/%s", redirect.From, fromPage)
			} else if anchors[fromPage][fromAnchor] {
				t.Errorf("anchor redirect %q shadows a heading that still exists", redirect.From)
			}
		} else if _, ok := pages[fromPage]; ok {
			t.Errorf("page redirect %q shadows an existing page", redirect.From)
		}
		if _, ok := pages[toPage]; !ok {
			t.Errorf("redirect %q targets missing docs/site/docs/%s", redirect.From, toPage)
		} else if toOK && !anchors[toPage][toAnchor] {
			t.Errorf("redirect %q targets missing anchor %q", redirect.From, redirect.To)
		}
	}
}

func TestSiteDocAnchorsMatchSiteSlugs(t *testing.T) {
	got := siteDocAnchors("# Title\n\n## Parity harness (only when `PIG_PARITY_HARNESS=1`)\n\n```\n## not a heading\n```\n\n### `pig docs`\n")
	for _, want := range []string{"title", "parity-harness-only-when-pig_parity_harness1", "pig-docs"} {
		if !got[want] {
			t.Errorf("siteDocAnchors missing %q in %v", want, got)
		}
	}
	if got["not-a-heading"] {
		t.Error("siteDocAnchors read a heading inside a code fence")
	}
}

var (
	siteDocAnyHeading = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*#*$`)
	siteSlugTags      = regexp.MustCompile(`<[^>]*>`)
	siteSlugDrop      = regexp.MustCompile(`[^\w\s-]`)
	siteSlugSpace     = regexp.MustCompile(`\s+`)
)

// siteDocAnchors returns the heading anchors the site renders for body. It
// mirrors the site's slugify in app/content.tsx.
func siteDocAnchors(body string) map[string]bool {
	anchors := map[string]bool{}
	fenced := false
	for line := range strings.SplitSeq(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		m := siteDocAnyHeading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		slug := strings.ToLower(m[1])
		slug = siteSlugTags.ReplaceAllString(slug, "")
		slug = siteSlugDrop.ReplaceAllString(slug, "")
		slug = siteSlugSpace.ReplaceAllString(strings.TrimSpace(slug), "-")
		anchors[slug] = true
	}
	return anchors
}

func readSiteDocsManifest(t *testing.T) siteDocsManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(siteDocsDir, "docs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest siteDocsManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("docs.json: %v", err)
	}
	if manifest.Navigation == nil {
		t.Fatal("docs.json must contain a navigation array")
	}
	if manifest.Provenance == nil {
		t.Fatal("docs.json must contain a provenance object")
	}
	return manifest
}

func readSiteDocPages(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(siteDocsDir)
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(siteDocsDir, name))
		if err != nil {
			t.Fatal(err)
		}
		pages[name] = string(body)
	}
	if len(pages) == 0 {
		t.Fatal("docs/site/docs has no pages; the check would pass vacuously")
	}
	return pages
}
