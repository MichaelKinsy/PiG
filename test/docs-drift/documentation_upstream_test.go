package docsdrift

// Ports packages/coding-agent/test/documentation.test.ts over PiG's own documentation, docs/site/docs.
//
// The upstream case reads the catalog (docs.json) and every Markdown page of the docs directory and requires: a valid catalog, no duplicate or missing navigation paths, no broken local Markdown links among the pages reachable from the navigation, no two pages with one public slug, and no orphaned page.
// Two catalog fields exist only in PiG's catalog and are accepted where upstream rejects unsupported fields: the top-level provenance map (siteDocsManifest) and a navigation item's pending flag, whose page may be absent until it exists.
// Link extraction walks the goldmark AST where upstream walks marked tokens; both yield inline and reference link destinations outside code, and neither yields images or raw HTML. An autolink always carries a scheme, which the walk skips, so it is not collected.

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

type documentationNavigationItem struct {
	path    string
	pending bool
	items   []documentationNavigationItem
}

func requireObject(value any, location string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", location)
	}
	return object, nil
}

func requireNonemptyString(value any, location string) (string, error) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s must be a nonempty string", location)
	}
	return text, nil
}

func requireFields(value map[string]any, fields []string, location string) error {
	var unsupported []string
	for field := range value {
		if !slices.Contains(fields, field) {
			unsupported = append(unsupported, field)
		}
	}
	slices.Sort(unsupported)
	if len(unsupported) > 0 {
		return fmt.Errorf("%s has unsupported fields: %s", location, strings.Join(unsupported, ", "))
	}
	return nil
}

var documentationWindowsDrive = regexp.MustCompile(`(?i)^[a-z]:`)

func requireDocumentationPath(value any, location string) (string, error) {
	documentationPath, err := requireNonemptyString(value, location)
	if err != nil {
		return "", err
	}
	normalized := path.Clean(documentationPath)
	escapesDocsRoot := normalized == ".." || strings.HasPrefix(normalized, "../")
	hasURLSyntax := strings.ContainsAny(documentationPath, "?#")
	hasWindowsSyntax := strings.Contains(documentationPath, `\`) || documentationWindowsDrive.MatchString(documentationPath)
	if documentationPath != strings.TrimSpace(documentationPath) || documentationPath != normalized || path.IsAbs(documentationPath) ||
		escapesDocsRoot || hasURLSyntax || hasWindowsSyntax || !strings.HasSuffix(documentationPath, ".md") {
		return "", fmt.Errorf("%s must be a relative normalized .md path", location)
	}
	return documentationPath, nil
}

func readNavigationItem(raw any, location string) (documentationNavigationItem, error) {
	item, err := requireObject(raw, location)
	if err != nil {
		return documentationNavigationItem{}, err
	}
	if err := requireFields(item, []string{"title", "path", "items", "pending"}, location); err != nil {
		return documentationNavigationItem{}, err
	}
	if _, err := requireNonemptyString(item["title"], location+".title"); err != nil {
		return documentationNavigationItem{}, err
	}
	result := documentationNavigationItem{}
	if value, present := item["path"]; present {
		if result.path, err = requireDocumentationPath(value, location+".path"); err != nil {
			return documentationNavigationItem{}, err
		}
	}
	if value, present := item["pending"]; present {
		pending, ok := value.(bool)
		if !ok {
			return documentationNavigationItem{}, fmt.Errorf("%s.pending must be a boolean", location)
		}
		result.pending = pending
	}
	if value, present := item["items"]; present {
		children, ok := value.([]any)
		if !ok {
			return documentationNavigationItem{}, fmt.Errorf("%s.items must be an array", location)
		}
		if len(children) == 0 {
			return documentationNavigationItem{}, fmt.Errorf("%s.items must contain at least one item", location)
		}
		for index, child := range children {
			parsed, err := readNavigationItem(child, fmt.Sprintf("%s.items[%d]", location, index))
			if err != nil {
				return documentationNavigationItem{}, err
			}
			result.items = append(result.items, parsed)
		}
	}
	if result.path == "" && result.items == nil {
		return documentationNavigationItem{}, fmt.Errorf("%s must have a path or items", location)
	}
	return result, nil
}

func readNavigation(docsRoot string) ([]documentationNavigationItem, error) {
	data, err := os.ReadFile(filepath.Join(docsRoot, "docs.json"))
	if err != nil {
		return nil, err
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	catalog, err := requireObject(parsed, "Documentation catalog")
	if err != nil {
		return nil, err
	}
	if err := requireFields(catalog, []string{"navigation", "redirects", "provenance"}, "Documentation catalog"); err != nil {
		return nil, err
	}
	groups, ok := catalog["navigation"].([]any)
	if !ok {
		return nil, fmt.Errorf("Documentation catalog navigation must be an array")
	}
	var roots []documentationNavigationItem
	for groupIndex, rawGroup := range groups {
		location := fmt.Sprintf("Documentation catalog navigation[%d]", groupIndex)
		group, err := requireObject(rawGroup, location)
		if err != nil {
			return nil, err
		}
		if err := requireFields(group, []string{"title", "items"}, location); err != nil {
			return nil, err
		}
		if _, err := requireNonemptyString(group["title"], location+".title"); err != nil {
			return nil, err
		}
		items, ok := group["items"].([]any)
		if !ok {
			return nil, fmt.Errorf("%s.items must be an array", location)
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("%s.items must contain at least one item", location)
		}
		for itemIndex, rawItem := range items {
			item, err := readNavigationItem(rawItem, fmt.Sprintf("%s.items[%d]", location, itemIndex))
			if err != nil {
				return nil, err
			}
			roots = append(roots, item)
		}
	}
	return roots, nil
}

func navigationPaths(items []documentationNavigationItem) (paths []string, pendingPaths map[string]bool) {
	pendingPaths = map[string]bool{}
	var visit func([]documentationNavigationItem)
	visit = func(items []documentationNavigationItem) {
		for _, item := range items {
			if item.path != "" {
				paths = append(paths, item.path)
				if item.pending {
					pendingPaths[item.path] = true
				}
			}
			visit(item.items)
		}
	}
	visit(items)
	return paths, pendingPaths
}

var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM))

func markdownLinks(markdown []byte) []string {
	var links []string
	_ = ast.Walk(markdownParser.Parser().Parse(text.NewReader(markdown)), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if link, ok := node.(*ast.Link); ok {
			links = append(links, string(link.Destination))
		}
		return ast.WalkContinue, nil
	})
	return links
}

func isFile(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular()
}

func publicSlug(page string) string {
	withoutExtension := strings.TrimSuffix(page, ".md")
	if withoutExtension == "index" {
		return ""
	}
	return strings.TrimSuffix(withoutExtension, "/index")
}

var documentationURLScheme = regexp.MustCompile(`(?i)^[a-z][a-z\d+.-]*:`)

// documentationIssues returns the navigation, link, slug and orphan problems of a docs directory.
func documentationIssues(docsRoot string) ([]string, error) {
	var allPages []string
	// fs.globSync("**/*.md") matches no dot-prefixed file or directory.
	if err := filepath.WalkDir(docsRoot, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name != docsRoot && strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			return nil
		}
		relative, err := filepath.Rel(docsRoot, name)
		allPages = append(allPages, filepath.ToSlash(relative))
		return err
	}); err != nil {
		return nil, err
	}
	slices.Sort(allPages)

	navigation, err := readNavigation(docsRoot)
	if err != nil {
		return nil, err
	}
	roots, pending := navigationPaths(navigation)
	var duplicateRoots, missingRoots []string
	for index, root := range roots {
		if slices.Index(roots, root) != index && !slices.Contains(duplicateRoots, root) {
			duplicateRoots = append(duplicateRoots, root)
		}
		if !isFile(filepath.Join(docsRoot, root)) && !pending[root] {
			missingRoots = append(missingRoots, root)
		}
	}
	slices.Sort(duplicateRoots)
	slices.Sort(missingRoots)

	pathsBySlug := map[string][]string{}
	var slugs []string
	for _, page := range allPages {
		slug := publicSlug(page)
		if _, seen := pathsBySlug[slug]; !seen {
			slugs = append(slugs, slug)
		}
		pathsBySlug[slug] = append(pathsBySlug[slug], page)
	}
	var slugCollisions []string
	for _, slug := range slugs {
		if len(pathsBySlug[slug]) > 1 {
			label := slug
			if label == "" {
				label = "/"
			}
			slugCollisions = append(slugCollisions, fmt.Sprintf("%s (%s)", label, strings.Join(pathsBySlug[slug], ", ")))
		}
	}
	slices.Sort(slugCollisions)

	reachable := map[string]bool{}
	var queue []string
	for _, root := range roots {
		if isFile(filepath.Join(docsRoot, root)) {
			queue = append(queue, root)
		}
	}
	var brokenLinks []string
	for len(queue) > 0 {
		source := queue[0]
		queue = queue[1:]
		if reachable[source] {
			continue
		}
		reachable[source] = true
		body, err := os.ReadFile(filepath.Join(docsRoot, source))
		if err != nil {
			return nil, err
		}
		for _, href := range markdownLinks(body) {
			if documentationURLScheme.MatchString(href) || strings.HasPrefix(href, "//") || strings.HasPrefix(href, "/") {
				continue
			}
			linkedPath := href
			if end := strings.IndexAny(href, "?#"); end >= 0 {
				linkedPath = href[:end]
			}
			if !strings.HasSuffix(linkedPath, ".md") {
				continue
			}
			target := filepath.Join(docsRoot, filepath.Dir(source), linkedPath)
			if !isFile(target) {
				brokenLinks = append(brokenLinks, source+" -> "+href)
				continue
			}
			relative, err := filepath.Rel(docsRoot, target)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
				continue
			}
			if normalized := filepath.ToSlash(relative); !reachable[normalized] {
				queue = append(queue, normalized)
			}
		}
	}

	var orphaned []string
	for _, page := range allPages {
		if !reachable[page] {
			orphaned = append(orphaned, page)
		}
	}
	slices.Sort(brokenLinks)
	var issues []string
	if len(duplicateRoots) > 0 {
		issues = append(issues, "Duplicate navigation paths: "+strings.Join(duplicateRoots, ", "))
	}
	if len(missingRoots) > 0 {
		issues = append(issues, "Missing navigation pages: "+strings.Join(missingRoots, ", "))
	}
	if len(brokenLinks) > 0 {
		issues = append(issues, "Broken local Markdown links: "+strings.Join(brokenLinks, ", "))
	}
	if len(slugCollisions) > 0 {
		issues = append(issues, "Duplicate public documentation slugs: "+strings.Join(slugCollisions, "; "))
	}
	if len(orphaned) > 0 {
		issues = append(issues, "Orphaned Markdown pages: "+strings.Join(orphaned, ", "))
	}
	return issues, nil
}

func TestCodingAgentDocumentationHasValidNavigationAndNoOrphanedMarkdownPages(t *testing.T) {
	// upstream: packages/coding-agent/test/documentation.test.ts:132 "has valid navigation and no orphaned Markdown pages"
	issues, err := documentationIssues(siteDocsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Fatalf("documentation issues:\n%s", strings.Join(issues, "\n"))
	}
}

func writeDocumentationTree(t *testing.T, catalog string, pages map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docs.json"), []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, body := range pages {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// The upstream case has one passing input. These cases give each rule an input that must fail, so the port cannot pass vacuously.
func TestDocumentationIssuesReportsEachRuleViolation(t *testing.T) {
	const catalog = `{"navigation":[{"title":"G","items":[{"title":"Home","path":"index.md"},{"title":"A","path":"a.md"},{"title":"A again","path":"a.md"},{"title":"Gone","path":"gone.md"}]}],"redirects":[]}`
	root := writeDocumentationTree(t, catalog, map[string]string{
		"index.md":     "# Home\n\n[a](a.md) [broken](missing.md#x) [external](https://example.com/x.md) [ref][r] ![image](pic.md)\n\n```\n[code](code-only.md)\n```\n\n[r]: sub/deep.md?x=1\n",
		"a.md":         "# A\n",
		"sub/deep.md":  "# Deep\n\n[up](../a.md) <https://example.com/auto.md>\n",
		"orphan.md":    "# Orphan\n",
		"sub/index.md": "# Sub\n",
		"code-only.md": "# only linked from a code block\n",
		"pic.md":       "# only an image target\n",
		".hidden.md":   "# a dotfile that fs.globSync does not match\n",
		".drafts/x.md": "# inside a dot directory\n",
	})
	issues, err := documentationIssues(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Duplicate navigation paths: a.md",
		"Missing navigation pages: gone.md",
		"Broken local Markdown links: index.md -> missing.md#x",
		"Orphaned Markdown pages: code-only.md, orphan.md, pic.md, sub/index.md",
	}
	if !slices.Equal(issues, want) {
		t.Fatalf("issues = %q\nwant %q", issues, want)
	}
}

func TestDocumentationIssuesReportsSlugCollisions(t *testing.T) {
	root := writeDocumentationTree(t, `{"navigation":[{"title":"G","items":[{"title":"Home","path":"index.md"},{"title":"Sub","path":"sub/index.md"}]}]}`, map[string]string{
		"index.md":     "# Home\n\n[sub](sub/index.md) [flat](sub.md)\n",
		"sub/index.md": "# Sub\n",
		"sub.md":       "# Flat\n",
	})
	issues, err := documentationIssues(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Duplicate public documentation slugs: sub (sub.md, sub/index.md)"}; !slices.Equal(issues, want) {
		t.Fatalf("issues = %q\nwant %q", issues, want)
	}
}

func TestDocumentationCatalogRejectsInvalidShapes(t *testing.T) {
	for name, catalog := range map[string]string{
		"unsupported catalog field": `{"navigation":[],"redirects":[],"extra":1}`,
		"navigation not an array":   `{"navigation":{}}`,
		"empty group":               `{"navigation":[{"title":"G","items":[]}]}`,
		"item without path":         `{"navigation":[{"title":"G","items":[{"title":"X"}]}]}`,
		"empty nested items":        `{"navigation":[{"title":"G","items":[{"title":"X","items":[]}]}]}`,
		"unsupported item field":    `{"navigation":[{"title":"G","items":[{"title":"X","path":"a.md","icon":"x"}]}]}`,
		"blank title":               `{"navigation":[{"title":" ","items":[{"title":"X","path":"a.md"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readNavigation(writeDocumentationTree(t, catalog, nil)); err == nil {
				t.Fatal("catalog was accepted")
			}
		})
	}
	for _, bad := range []string{"/abs.md", "../up.md", "a/../b.md", "a.md#x", "a.md?x", `a\b.md`, "c:a.md", " a.md", "a.txt", "./a.md", ""} {
		if _, err := requireDocumentationPath(bad, "path"); err == nil {
			t.Errorf("path %q was accepted", bad)
		}
	}
	if _, err := requireDocumentationPath("sub/page.md", "path"); err != nil {
		t.Errorf("a normalized relative path was rejected: %v", err)
	}
}
