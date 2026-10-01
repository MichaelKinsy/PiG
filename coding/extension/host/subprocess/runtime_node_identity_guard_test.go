package subprocess_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// piIdentityPatterns find the literals Pi uses to identify itself to services
// or to reach Pi-owned endpoints. Every match in the vendored runtime must be
// covered by an allowlist entry that says why it stays.
var piIdentityPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[A-Za-z0-9.-]*pi\.dev[^\s"'` + "`" + `)]*`),
	regexp.MustCompile(`earendil-works/pi([^-A-Za-z]|$)`),
	regexp.MustCompile(`mariozechner\.at`),
	regexp.MustCompile(`(?i)"(x-opencode-client|x-openrouter-title|x-billing-invoke-origin)"\s*:\s*"pi"`),
	regexp.MustCompile(`"User-Agent"\s*:\s*"pi-coding-agent"`),
	regexp.MustCompile(`originator\s*=\s*"pi"`),
	regexp.MustCompile(`referrer\s*:\s*"pi"`),
	regexp.MustCompile(`"pi-gateway"`),
	regexp.MustCompile("`pi[ /]\\(?\\$\\{[^`]*`"),
}

type identityAllowance struct {
	file   *regexp.Regexp
	match  *regexp.Regexp
	reason string
	used   bool
}

// The vendored Pi runtime keeps only these Pi identity literals. Each is either
// the same value the Go host uses by design, or text PiG never sends.
func identityAllowances() []*identityAllowance {
	allow := func(file, match, reason string) *identityAllowance {
		return &identityAllowance{file: regexp.MustCompile(file), match: regexp.MustCompile(match), reason: reason}
	}
	const sameAsGo = "Radius is Pi's model gateway, selected only when the user logs in to the radius provider; the Go host ships the same catalog and gateway (ai/radius_config.go, ai/models_generated.go)."
	const docLink = "documentation link or issue reference in a comment or message text; it is never requested."
	return []*identityAllowance{
		allow(`^pi-ai/(sdk-bundle/chunk-[A-Z0-9]+\.js|providers/data/radius\.json)$`, `^radius\.pi\.dev/v1$`, sameAsGo),
		allow(`^pi-ai/providers/radius-config\.js$`, `^radius\.pi\.dev$`, sameAsGo),
		allow(`^pi-ai/auth/oauth/radius\.js$`, `^"pi-gateway"$`, "the OAuth client id Radius registered for its gateway; the Go host sends the same one (ai/oauth_radius.go)."),
		allow(`^pi-ai/api/openai-responses-shared\.js$`, `^earendil-works/pi`, docLink),
		allow(`^pi-tui/components/markdown\.js$`, `^earendil-works/pi`, docLink),
		allow(`^pi-coding-agent/(utils/child-process|core/tools/find)\.js$`, `^earendil-works/pi`, docLink),
		allow(`^pi-coding-agent/utils/changelog\.js$`, `^earendil-works/pi`, "GITHUB_REPO only formats pull-request links inside changelog text; nothing is requested."),
		allow(`^pi-coding-agent/(migrations\.js|sdk-bundle/index\.js)$`, `^earendil-works/pi`, "migration guide links printed to the user; the Go host prints the same ones (internal/codingagent/migrations.go)."),
		allow(`^pi-coding-agent/(config\.js|sdk-bundle/chunk-[A-Z0-9]+\.js)$`, `^earendil-works/pi`, "install-method message text (Download from ...); never requested."),
		allow(`^pi-coding-agent/package\.json$`, `^earendil-works/pi`, "the pinned package's repository field."),
		allow(`^pi-coding-agent/modes/interactive/theme/(dark|light)\.json$`, `^earendil-works/pi`, "the theme's $schema reference, read by editors."),
		allow(`^pi-coding-agent/core/remote-catalog-provider\.js$`, `^pi\.dev$`, "a doc comment naming the catalog the overlay replaces; the request goes to PiG's hosted origin (D64)."),
		allow(`^pi-coding-agent/(config\.js|cli/args\.js|sdk-bundle/chunk-[A-Z0-9]+\.js)$`, `pi\.dev/session/`, "the /share gist viewer address shown as text; PiG's /share uses its own gateway (D64) and never opens this viewer."),
		allow(`^pi-coding-agent/(modes/interactive/interactive-mode\.js|sdk-bundle/index\.js)$`, `pi\.dev/changelog`, "the update notification prints this link; PiG's Go notification drops it (D39) and nothing requests it."),
		allow(`^pi-coding-agent/(modes/interactive/components/earendil-announcement\.js|sdk-bundle/index\.js)$`, `mariozechner\.at`, "the announcement banner's blog link, shown as text; the Go host shows the same (internal/codingagent/earendil_announcement.go)."),
	}
}

// Text files of the vendored runtime that can run or be read as source. Pi's
// documentation and example extensions are reference material, not runtime.
func scanVendoredRuntimeFile(rel string) bool {
	switch {
	case strings.HasPrefix(rel, "pi-coding-agent/docs/"), strings.HasPrefix(rel, "pi-coding-agent/examples/"):
		return false
	case strings.HasSuffix(rel, "/CHANGELOG.md"), strings.HasSuffix(rel, "/README.md"), strings.HasSuffix(rel, "/inputs.json"):
		return false
	}
	return strings.HasSuffix(rel, ".js") || strings.HasSuffix(rel, ".mjs") || strings.HasSuffix(rel, ".json")
}

// A Node extension that calls the vendored SDK or pi-ai runs Pi's own code, so
// no Pi identity literal may survive in it unless it is on the allowlist above
// with a reason (D26). This is the guard for a re-vendor: a new Pi release that
// adds an identity literal fails here until it is patched or allowlisted.
func TestVendoredRuntimeCarriesNoPiIdentityOutsideTheAllowlist(t *testing.T) {
	root := filepath.Join("runtime-node", "shims", "pi-dist")
	allowances := identityAllowances()
	scanned := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !scanVendoredRuntimeFile(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for _, pattern := range piIdentityPatterns {
			for _, match := range pattern.FindAllString(string(data), -1) {
				covered := false
				for _, allowance := range allowances {
					if allowance.file.MatchString(rel) && allowance.match.MatchString(match) {
						allowance.used = true
						covered = true
						break
					}
				}
				if !covered {
					t.Errorf("%s carries Pi identity %q: patch it in automation/gen/pi-identity-patches.mjs or allow it with a reason", rel, match)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 300 {
		t.Fatalf("scanned %d vendored files, expected the whole runtime", scanned)
	}
	for _, allowance := range allowances {
		if !allowance.used {
			t.Errorf("allowlist entry %s %s is stale: %s", allowance.file, allowance.match, allowance.reason)
		}
	}
}
