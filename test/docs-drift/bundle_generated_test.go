package docsdrift

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The agent docs bundle (internal/pigdocs/content) copies the pages it shares with the site from docs/site/docs. The generator automation/gen/gen-docs-bundle.py writes those copies from internal/pigdocs/bundle-manifest.json. These tests fail when a generated page is edited by hand, when a bundle page has no owner, and when a bundle link leaves the flat bundle.

const (
	bundleGenerator   = "../../automation/gen/gen-docs-bundle.py"
	bundleManifestRel = "internal/pigdocs/bundle-manifest.json"
	bundleDirRel      = "internal/pigdocs/content"
)

type bundleManifest struct {
	Pages []struct {
		Site   string `json:"site"`
		Bundle string `json:"bundle"`
	} `json:"pages"`
	Glossary struct {
		Source string `json:"source"`
		Site   string `json:"site"`
		Bundle string `json:"bundle"`
	} `json:"glossary"`
	Blocks []struct {
		Name    string `json:"name"`
		Targets []struct {
			File string `json:"file"`
		} `json:"targets"`
	} `json:"blocks"`
	AgentOnly       []string `json:"agent_only"`
	DeferredPairs   []string `json:"deferred_pairs"`
	OtherGenerators []string `json:"other_generators"`
}

func readBundleManifest(t *testing.T) bundleManifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, bundleManifestRel))
	if err != nil {
		t.Fatal(err)
	}
	var m bundleManifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", bundleManifestRel, err)
	}
	return m
}

func runBundleGenerator(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(bundleGenerator)
	if err != nil {
		t.Fatal(err)
	}
	args = append([]string{script, "--root", root}, args...)
	out, err := exec.Command("python3", args...).CombinedOutput()
	return string(out), err
}

// scratchBundleRoot copies what the generator reads into a temporary checkout.
func scratchBundleRoot(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	for _, rel := range []string{"docs/site/docs", "docs/site/fragments", "docs/project", bundleDirRel, bundleManifestRel} {
		from := filepath.Join(repoRoot, filepath.FromSlash(rel))
		to := filepath.Join(dst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("cp", "-R", from, to).CombinedOutput(); err != nil {
			t.Fatalf("copy %s: %v\n%s", rel, err, out)
		}
	}
	return dst
}

func TestGeneratedBundlePagesAreCurrent(t *testing.T) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runBundleGenerator(t, root, "--check"); err != nil {
		t.Fatalf("generated docs pages are stale or unclassified:\n%s", out)
	}
}

func TestGeneratedBundlePageEditedByHandFails(t *testing.T) {
	m := readBundleManifest(t)
	if len(m.Pages) == 0 {
		t.Fatal("the manifest has no generated pages")
	}
	root := scratchBundleRoot(t)
	if out, err := runBundleGenerator(t, root, "--check"); err != nil {
		t.Fatalf("the scratch copy is not current:\n%s", out)
	}
	page := m.Pages[0].Bundle
	path := filepath.Join(root, filepath.FromSlash(bundleDirRel), page)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, "A hand edit.\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runBundleGenerator(t, root, "--check")
	if err == nil || !strings.Contains(out, page) {
		t.Fatalf("a hand edit of %s must fail --check and name the page; err=%v output=%s", page, err, out)
	}
}

func TestGeneratedBlockEditedByHandFails(t *testing.T) {
	m := readBundleManifest(t)
	if len(m.Blocks) == 0 || len(m.Blocks[0].Targets) == 0 {
		t.Fatal("the manifest has no generated blocks")
	}
	root := scratchBundleRoot(t)
	target := m.Blocks[0].Targets[len(m.Blocks[0].Targets)-1].File
	path := filepath.Join(root, filepath.FromSlash(target))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := "<!-- gen:end " + m.Blocks[0].Name + " -->"
	edited := strings.Replace(string(body), marker, "A hand edit.\n"+marker, 1)
	if edited == string(body) {
		t.Fatalf("%s has no %s", target, marker)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runBundleGenerator(t, root, "--check"); err == nil || !strings.Contains(out, target) {
		t.Fatalf("a hand edit inside a generated block must fail --check and name %s; err=%v output=%s", target, err, out)
	}
}

func TestSiteEditOfASharedPageStalesTheBundle(t *testing.T) {
	m := readBundleManifest(t)
	root := scratchBundleRoot(t)
	path := filepath.Join(root, "docs", "site", "docs", m.Pages[0].Site)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, "\nA new site sentence.\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runBundleGenerator(t, root, "--check"); err == nil || !strings.Contains(out, m.Pages[0].Bundle) {
		t.Fatalf("a site edit must stale %s; err=%v output=%s", m.Pages[0].Bundle, err, out)
	}
}

func TestEveryBundlePageHasOneOwner(t *testing.T) {
	m := readBundleManifest(t)
	owners := map[string]string{}
	claim := func(name, owner string) {
		if prev, ok := owners[name]; ok {
			t.Errorf("%s is claimed by %s and %s", name, prev, owner)
		}
		owners[name] = owner
	}
	for _, p := range m.Pages {
		claim(p.Bundle, "pages")
	}
	claim(m.Glossary.Bundle, "glossary")
	for _, n := range m.AgentOnly {
		claim(n, "agent_only")
	}
	for _, n := range m.DeferredPairs {
		claim(n, "deferred_pairs")
	}
	for _, n := range m.OtherGenerators {
		claim(n, "other_generators")
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot, bundleDirRel))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if _, ok := owners[e.Name()]; !ok && strings.HasSuffix(e.Name(), ".md") {
			t.Errorf("%s/%s has no owner in %s", bundleDirRel, e.Name(), bundleManifestRel)
		}
	}
	for name := range owners {
		if _, err := os.Stat(filepath.Join(repoRoot, bundleDirRel, name)); err != nil {
			t.Errorf("the manifest names %s, which the bundle lacks", name)
		}
	}
	// A deferred pair is a hand-written page that still has a differing site page.
	for _, n := range m.DeferredPairs {
		if _, err := os.Stat(filepath.Join(siteDocsDir, n)); err != nil {
			t.Errorf("%s is a deferred pair but the site has no %s", n, n)
		}
	}
}

var generatedHeader = regexp.MustCompile(`^<!-- Generated from \S+ by automation/gen/gen-docs-bundle\.py\. Do not edit this file`)

func TestGeneratedBundlePagesCarryTheDoNotEditHeader(t *testing.T) {
	m := readBundleManifest(t)
	names := []string{m.Glossary.Bundle}
	for _, p := range m.Pages {
		names = append(names, p.Bundle)
	}
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(repoRoot, bundleDirRel, name))
		if err != nil {
			t.Fatal(err)
		}
		if !generatedHeader.Match(body) {
			t.Errorf("%s is generated and must start with the do-not-edit header", name)
		}
	}
}

// A bundle page is read from a flat directory by `pig docs show`, so a link must name a bundle page, an anchor, or a full URL.
func TestBundleLinksStayInsideTheFlatBundle(t *testing.T) {
	flat := regexp.MustCompile(`^[A-Za-z0-9-]+\.md(#[^/]*)?$`)
	for _, src := range corpus(t) {
		if !strings.HasPrefix(src.Rel, bundleDirRel+"/") {
			continue
		}
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
				target := strings.Trim(m[1], "<>")
				switch {
				case strings.HasPrefix(target, "#"), strings.HasPrefix(target, "http://"), strings.HasPrefix(target, "https://"), strings.HasPrefix(target, "mailto:"):
				case flat.MatchString(target):
				default:
					t.Errorf("%s: link %q leaves the flat bundle; use a bundle page name or a full URL", src.Rel, target)
				}
			}
		}
	}
}

// The glossary has one H2 for every term of docs/project/CONTEXT.md, on the site and in the bundle.
func TestGlossaryHasEveryContextTerm(t *testing.T) {
	m := readBundleManifest(t)
	ctx, err := os.ReadFile(filepath.Join(repoRoot, m.Glossary.Source))
	if err != nil {
		t.Fatal(err)
	}
	terms := regexp.MustCompile(`(?m)^## (.+?)\s*$`).FindAllStringSubmatch(string(ctx), -1)
	if len(terms) < 19 {
		t.Fatalf("found %d terms in %s; the extractor is broken", len(terms), m.Glossary.Source)
	}
	pages := map[string]string{
		"docs/site/docs/" + m.Glossary.Site:    filepath.Join(siteDocsDir, m.Glossary.Site),
		bundleDirRel + "/" + m.Glossary.Bundle: filepath.Join(repoRoot, bundleDirRel, m.Glossary.Bundle),
	}
	for label, path := range pages {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range terms {
			if !regexp.MustCompile(`(?m)^## ` + regexp.QuoteMeta(want[1]) + `\s*$`).Match(body) {
				t.Errorf("%s has no H2 for the CONTEXT.md term %q", label, want[1])
			}
		}
	}
}

// Pi links its own files on the main branch. A tag pins a page to a Pi release that the text beside it no longer describes.
var piTagLink = regexp.MustCompile(`github\.com/earendil-works/pi/(?:blob|tree)/v[0-9][^/\s)]*/`)

func TestNoLinkPinsAStalePiTag(t *testing.T) {
	for _, src := range corpus(t) {
		if loc := piTagLink.FindString(src.Body); loc != "" {
			t.Errorf("%s links a Pi tag (%s); link blob/main as Pi's own docs do", src.Rel, loc)
		}
	}
}
