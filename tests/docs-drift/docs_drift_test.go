// Package docsdrift gates the shipped documentation bundle against the code it
// describes.
//
// The bundle in internal/pigdocs/content is what a user reads and
// what an agent loads to answer questions about Pig, so a stale claim there is
// not cosmetic: it sends both down a path that no longer exists. Prose intent
// cannot be machine-checked, but the enumerable claims can be, and those are the
// ones that rot silently as commands and settings move.
//
// Every check derives its expected set from the production source rather than a
// list maintained here, so the gate cannot drift from the thing it guards.
package docsdrift

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const contentDir = "../../internal/pigdocs/content"

func docFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(contentDir)
	if err != nil {
		t.Fatalf("read docs bundle: %v", err)
	}
	out := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(contentDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		out[entry.Name()] = string(data)
	}
	if len(out) == 0 {
		t.Fatal("docs bundle is empty; the gate would pass vacuously")
	}
	return out
}

// Slash commands are matched only inside backticks. A bare "/foo" in prose
// collides with paths and regexes, which would make the gate noisy enough to be
// switched off.
var backtickedSlash = regexp.MustCompile("`(/[a-z][a-z0-9-]*)(?: [^`]*)?`")

func builtinNames(t *testing.T) (all map[string]bool, listable []string) {
	t.Helper()
	all = map[string]bool{}
	for _, cmd := range codingagent.BuiltinSlashCommands() {
		all["/"+cmd.Name] = true
		for _, alias := range cmd.Aliases {
			all["/"+alias] = true
		}
		if !cmd.Hidden {
			listable = append(listable, "/"+cmd.Name)
		}
	}
	sort.Strings(listable)
	return all, listable
}

// A documented command that does not exist is the worse of the two failures: the
// reader types it and it does nothing.
func TestDocumentedSlashCommandsExist(t *testing.T) {
	known, _ := builtinNames(t)
	// Commands contributed by extensions rather than the builtin registry. Each
	// entry needs the extension that provides it, so an obsolete one is visible.
	// Commands the docs name in order to say they do not exist. Pi has neither,
	// and readers arrive expecting both, so the absence is worth stating.
	documentedAsAbsent := map[string]string{
		"/exit":       "Pi has no /exit; use /quit",
		"/clear":      "Pi has no /clear; app.clear is a keybinding",
		"/runner":     "Stock Pig has no runner command",
		"/pig-runner": "Stock Pig has no runner alias",
	}
	fromExtensions := map[string]string{
		"/piglet":    "generic Piglet runtime extension",
		"/sprite":    "PiG Standard piglogin extension",
		"/review":    "example prompt template a user creates (prompt-templates.md)",
		"/component": "example prompt template a user creates (prompt-templates.md)",
	}
	for name, body := range docFiles(t) {
		for _, match := range backtickedSlash.FindAllStringSubmatch(body, -1) {
			cmd := match[1]
			if known[cmd] || fromExtensions[cmd] != "" || documentedAsAbsent[cmd] != "" {
				continue
			}
			t.Errorf("%s documents %s, which is not a builtin command and has no "+
				"recorded extension source; remove it or record where it comes from", name, cmd)
		}
	}
}

// The reverse direction finds the silent gap: a shipped command nobody wrote
// down. /help lists it, so a user sees it and finds nothing in the docs.
func TestListableSlashCommandsAreDocumented(t *testing.T) {
	_, listable := builtinNames(t)
	var joined strings.Builder
	for _, body := range docFiles(t) {
		joined.WriteString(body)
	}
	all := joined.String()

	documentedCmds := map[string]bool{}
	for _, match := range backtickedSlash.FindAllStringSubmatch(all, -1) {
		documentedCmds[match[1]] = true
	}
	documented := func(_ string, cmd string) bool { return documentedCmds[cmd] }

	var undocumented []string
	for _, cmd := range listable {
		if !documented(all, cmd) {
			undocumented = append(undocumented, cmd)
		}
	}
	if len(undocumented) > 0 {
		t.Errorf("%d command(s) ship in /help but appear nowhere in the docs bundle:\n  %s",
			len(undocumented), strings.Join(undocumented, "\n  "))
	}
}

// A page nothing links to is a page nobody finds. README.md is the bundle index.
func TestEveryPageIsReachableFromTheIndex(t *testing.T) {
	files := docFiles(t)
	index, ok := files["README.md"]
	if !ok {
		t.Fatal("bundle has no README.md index")
	}
	for name := range files {
		if name == "README.md" {
			continue
		}
		if !strings.Contains(index, name) {
			t.Errorf("README.md does not link %s; the page is unreachable from the index", name)
		}
	}
}

func TestInternalLinksResolve(t *testing.T) {
	files := docFiles(t)
	link := regexp.MustCompile(`\]\(([A-Za-z0-9_.-]+\.md)(?:#[^)]*)?\)`)
	for name, body := range files {
		for _, match := range link.FindAllStringSubmatch(body, -1) {
			if _, ok := files[match[1]]; !ok {
				t.Errorf("%s links %s, which is not in the bundle", name, match[1])
			}
		}
	}
}

func TestGeneratedTroubleshootingLinksUseDocumentationSite(t *testing.T) {
	const siteDocs = "https://pi-in-go.dev/docs/latest/"

	source, err := os.ReadFile("../../docs/site/docs/install-troubleshooting.md")
	if err != nil {
		t.Fatalf("read troubleshooting source: %v", err)
	}
	files := docFiles(t)
	generated, ok := files["troubleshooting.md"]
	if !ok {
		t.Fatal("docs bundle has no generated troubleshooting.md")
	}

	sourceLink := regexp.MustCompile(`\]\(/docs/latest/([a-z0-9-]+)\)`)
	matches := sourceLink.FindAllStringSubmatch(string(source), -1)
	if len(matches) == 0 {
		t.Fatal("troubleshooting source has no documentation links; the gate would pass vacuously")
	}
	for _, match := range matches {
		slug := match[1]
		expected := siteDocs + slug + "/"
		if _, bundled := files[slug+".md"]; bundled {
			expected = slug + ".md"
		}
		if !strings.Contains(generated, "]("+expected+")") {
			t.Errorf("generated troubleshooting.md does not map /docs/latest/%s to %s", slug, expected)
		}
	}
	if strings.Contains(generated, "michaelkinsy.github.io") {
		t.Error("generated troubleshooting.md still links the retired GitHub Pages site")
	}
}

// Renaming a command and leaving the old name in the docs is the most common way
// this bundle goes stale, and the reader has no way to tell which is current.
func TestNoRetiredCommandNamesSurvive(t *testing.T) {
	retired := map[string]string{
		"pig sdk":       "replaced by `pig reload`",
		"pig --profile": "replaced by `pig --piglet`",
		"pig profile":   "replaced by `pig piglet`",
		"`/profile`":    "replaced by `/piglet`",
		// PIG_PROFILE and PIG_PROFILE_DIR are live Go profiling variables;
		// only the Piglet-selection names were retired.
		"PIG_PROFILE_NAME": "replaced by `PIG_PIGLET_NAME`",
		"PIG_PROFILE_PATH": "replaced by `PIG_PIGLET_PATH`",
		"profile.yaml":     "replaced by `piglet.yaml`",
	}
	for name, body := range docFiles(t) {
		for phrase, replacement := range retired {
			if strings.Contains(body, phrase) {
				t.Errorf("%s still references %q (%s)", name, phrase, replacement)
			}
		}
	}
}

func TestPigOwnedInstructionsUsePiglets(t *testing.T) {
	paths := []string{
		"../../docs/additive-features.md",
	}
	retired := []string{
		"pig --profile",
		"pig profile",
		"`/profile`",
		"PIG_PROFILE_",
		"/opt/pig/profile.yaml",
		"Profile Binary",
		"Profile Image",
		"Pig Profile",
	}
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, phrase := range retired {
			if strings.Contains(string(body), phrase) {
				t.Errorf("%s still names retired Pig capability %q", path, phrase)
			}
		}
	}
}

// A divergence cited in the docs must exist in the ledger, and a user-visible
// divergence the ledger approves should be findable from the docs. Without the
// first check a page can promise behavior no longer approved; without the
// second, an approved difference reaches nobody. Agents are the reason this
// matters most: one that carries Pi knowledge will confidently do the Pi thing
// wherever Pig's difference is undocumented.
func TestDocumentedDivergenceIDsExistInTheLedger(t *testing.T) {
	// IDs are global across the parity ledger and the additive ledger, so both
	// are the denominator. Reading one alone reports a true entry as missing.
	declared := map[string]bool{}
	for _, ledger := range []string{"../../DIVERGENCES.md", "../../docs/additive-features.md"} {
		data, err := os.ReadFile(ledger)
		if err != nil {
			t.Fatalf("read %s: %v", ledger, err)
		}
		for _, match := range regexp.MustCompile(`(?m)^#+ *(D\d+)\b`).FindAllStringSubmatch(string(data), -1) {
			declared[match[1]] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("no divergence IDs parsed from the ledger; the gate would pass vacuously")
	}

	cited := regexp.MustCompile(`\bD(\d+)\b`)
	for name, body := range docFiles(t) {
		for _, match := range cited.FindAllStringSubmatch(body, -1) {
			id := "D" + match[1]
			if !declared[id] {
				t.Errorf("%s cites %s, which DIVERGENCES.md does not declare", name, id)
			}
		}
	}
}
