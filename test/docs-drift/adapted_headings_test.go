package docsdrift

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// upstreamDocsDir is the pinned upstream Pi documentation in the local mirror.
const upstreamDocsDir = "../../.upstream/current/packages/coding-agent/docs"

// upstreamPageName maps a site page whose file name differs from its Pi page.
var upstreamPageName = map[string]string{"how-pig-works.md": "how-pi-works.md"}

// intentionalOmissions lists, per adapted-pi page, upstream section headings the PiG page intentionally lacks.
// Every entry needs a reason; a stale entry (the page has the heading again) fails the test.
var intentionalOmissions = map[string]map[string]string{
	"models.md": {
		"Choose a connection": "PiG connects providers through its own provider catalog and /login flow, so the upstream connection chooser does not apply; the page describes PiG's connections instead.",
		"Authenticate":        "PiG authentication differs from Pi's (its own credential store and /login flow); the page documents PiG's, so the upstream section is intentionally not kept.",
		"Troubleshooting":     "PiG's page names this section \"Common errors\" and lists PiG's own failure cases.",
	},
}

var markdownHeading = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)

// normalizeHeading folds the PiG/Pi name difference and formatting so only the wording is compared.
func normalizeHeading(h string) string {
	h = strings.NewReplacer("`", "", "*", "", "PiG", "Pi", "Pig", "Pi", "pig", "pi").Replace(h)
	return strings.ToLower(strings.Join(strings.Fields(h), " "))
}

func pageHeadings(body string) []string {
	var out []string
	fence := ""
	for line := range strings.SplitSeq(body, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			marker := trim[:3]
			switch fence {
			case "":
				fence = marker
			case marker:
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if m := markdownHeading.FindStringSubmatch(line); m != nil && len(m[1]) == 2 {
			out = append(out, m[2])
		}
	}
	return out
}

// TestAdaptedPiPagesKeepUpstreamHeadings requires every page classified adapted-pi to keep each section heading of the
// pinned upstream Pi page, unless intentionalOmissions says why the PiG page differs.
func TestAdaptedPiPagesKeepUpstreamHeadings(t *testing.T) {
	if _, err := os.Stat(upstreamDocsDir); err != nil {
		t.Skipf("the Pi source mirror is absent (%s); run `make upstream-mirror` to enable this check", upstreamDocsDir)
	}
	manifest := readSiteDocsManifest(t)
	pages := readSiteDocPages(t)
	used := map[string]map[string]bool{}
	for name, class := range manifest.Provenance {
		if class != "adapted-pi" {
			continue
		}
		upName := name
		if mapped, ok := upstreamPageName[name]; ok {
			upName = mapped
		}
		upstream, err := os.ReadFile(filepath.Join(upstreamDocsDir, upName))
		if err != nil {
			t.Errorf("%s is adapted-pi but the pinned Pi has no docs/%s (%v); reclassify it as pig-authored or map its upstream name", name, upName, err)
			continue
		}
		have := map[string]bool{}
		for _, h := range pageHeadings(pages[name]) {
			have[normalizeHeading(h)] = true
		}
		for _, h := range pageHeadings(string(upstream)) {
			if have[normalizeHeading(h)] {
				if _, ok := intentionalOmissions[name][h]; ok {
					t.Errorf("%s keeps %q, so its intentionalOmissions entry is stale", name, h)
				}
				continue
			}
			if reason, ok := intentionalOmissions[name][h]; ok && strings.TrimSpace(reason) != "" {
				if used[name] == nil {
					used[name] = map[string]bool{}
				}
				used[name][h] = true
				continue
			}
			t.Errorf("%s lacks the upstream Pi heading %q: restore the section, or list it in intentionalOmissions with the reason", name, h)
		}
	}
	for name, heads := range intentionalOmissions {
		for h := range heads {
			if !used[name][h] && manifest.Provenance[name] != "adapted-pi" {
				t.Errorf("intentionalOmissions names %s %q, but that page is not adapted-pi", name, h)
			}
		}
	}
}
