package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// assertStripBuildOmits checks that stock cmd/pig links pkg and a build with the feature's tag does not.
func assertStripBuildOmits(t *testing.T, feature, pkg string) {
	t.Helper()
	tag := pigstrip.Tag(feature)
	if !slices.Contains(goListCmdPig(t, "", "-deps"), pkg) {
		t.Errorf("the stock cmd/pig does not link %s", pkg)
	}
	if slices.Contains(goListCmdPig(t, tag, "-deps"), pkg) {
		t.Errorf("a %s build of cmd/pig links %s", tag, pkg)
	}
}

// A pig_strip_export_html build compiles internal/codingagent's session_export_html.go and coding/cli's export_flag.go out,
// so it does not link internal/codingagent/export with its HTML template, CSS, JS and vendored marked/highlight.js embeds.
func TestStripExportHTMLBuildOmitsExportPackage(t *testing.T) {
	assertStripBuildOmits(t, pigstrip.ExportHTML, "github.com/MichaelKinsy/PiG/internal/codingagent/export")
}

// A pig_strip_changelog build compiles internal/codingagent's changelog_bundle.go out, so it does not link the root
// package and its CHANGELOG.md embed.
func TestStripChangelogBuildOmitsRootPackage(t *testing.T) {
	assertStripBuildOmits(t, pigstrip.Changelog, "github.com/MichaelKinsy/PiG")
}

// A pig_strip_docs build compiles coding/cli's docs_command.go out, so it does not link internal/pigdocs and its
// content/*.md bundle.
func TestStripDocsBuildOmitsPigdocs(t *testing.T) {
	assertStripBuildOmits(t, pigstrip.Docs, "github.com/MichaelKinsy/PiG/internal/pigdocs")
}

// A pig_strip_piglet_builder build compiles coding/cli's piglet_builder_command.go out, so it does not link
// coding/pigletbuild.
func TestStripPigletBuilderBuildOmitsPigletbuild(t *testing.T) {
	assertStripBuildOmits(t, pigstrip.PigletBuilder, "github.com/MichaelKinsy/PiG/coding/pigletbuild")
}

// Self-update lives in coding/cli's self_update.go on internal/codingagent's update support, which other commands share, so
// a pig_strip_self_update build drops no package: it compiles self_update.go out for self_update_stripped.go.
func TestStripSelfUpdateBuildOmitsSelfUpdateFile(t *testing.T) {
	format := []string{"-f", "{{.GoFiles}}"}
	const cli = "github.com/MichaelKinsy/PiG/coding/cli"
	stock := goListPackage(t, cli, "", format...)
	if !slices.Contains(stock, "self_update.go") || slices.Contains(stock, "self_update_stripped.go") {
		t.Errorf("stock coding/cli files: %v", stock)
	}
	tagged := goListPackage(t, cli, pigstrip.Tag(pigstrip.SelfUpdate), format...)
	if slices.Contains(tagged, "self_update.go") || !slices.Contains(tagged, "self_update_stripped.go") {
		t.Errorf("pig_strip_self_update coding/cli files: %v", tagged)
	}
}

// A Piglet that strips docs, at runtime or by compiling them out of its Binary, answers `pig docs` with the stripped
// message and exit 1 and writes no bundle. Stock prints the docs directory.
func TestStripDocsCommandReportsStripped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	if !pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs) {
		stdout, _, code := captureStdoutStderr(t, func() int { return runPigPreSessionCommand([]string{"docs", "path"}) })
		if code != 0 || !strings.HasPrefix(stdout, home) {
			t.Fatalf("stock `pig docs path`: exit %d, stdout %q", code, stdout)
		}
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.Docs))
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runPigPreSessionCommand([]string{"docs", "path"}) })
	if want := "pig: pig docs is stripped from this Piglet (strip.features: docs)\n"; code != 1 || stdout != "" || stderr != want {
		t.Fatalf("stripped `pig docs path`: exit %d, stdout %q, stderr %q, want %q", code, stdout, stderr, want)
	}
	syncDocsBundle()
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("stripped startup sync wrote %v under PIG_HOME (err %v)", entries, err)
	}
}

// A Piglet that strips the Piglet builder, at runtime or by compiling it out of its Binary, answers `pig piglet build`
// and `pig piglet publish` with the stripped message and exit 1. Stock runs the builder.
func TestStripPigletBuilderCommandReportsStripped(t *testing.T) {
	if !pigstrip.Has(pigstrip.ListFeatures, pigstrip.PigletBuilder) {
		stdout, _, code := captureStdoutStderr(t, func() int { return runPigPreSessionCommand([]string{"piglet", "build", "--help"}) })
		if code != 0 || !strings.Contains(stdout, "pig piglet build <name>") {
			t.Fatalf("stock `pig piglet build --help`: exit %d, stdout %q", code, stdout)
		}
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.PigletBuilder))
	for _, sub := range []string{"build", "publish"} {
		stdout, stderr, code := captureStdoutStderr(t, func() int { return runPigPreSessionCommand([]string{"piglet", sub, "--help"}) })
		want := "pig: pig piglet " + sub + " is stripped from this Piglet (strip.features: piglet-builder); use stock pig\n"
		if code != 1 || stdout != "" || stderr != want {
			t.Fatalf("stripped `pig piglet %s`: exit %d, stdout %q, stderr %q, want %q", sub, code, stdout, stderr, want)
		}
	}
}

// A Piglet that strips self-update, at runtime or by compiling it out of its Binary, refuses `pig update` with the
// stripped message and exit 1 and shows no startup new-version notice. Stock resolves an installation tier.
func TestStripSelfUpdateRefusesUpdate(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_UPDATE_URL", "")
	if !pigstrip.Has(pigstrip.ListFeatures, pigstrip.SelfUpdate) {
		_, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
		if code != 1 || !strings.Contains(stderr, "cannot self-update this installation") {
			t.Fatalf("stock unreceipted update: exit %d, stderr %q", code, stderr)
		}
		if binaryUpdateChecker() == nil {
			t.Fatal("stock interactive mode has no binary update check")
		}
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.SelfUpdate))
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runSelfUpdate(false) })
	want := "pig: self-update is stripped from this Piglet (strip.features: self-update); update it through its Piglet distribution\n"
	if code != 1 || stdout != "" || stderr != want {
		t.Fatalf("stripped update: exit %d, stdout %q, stderr %q, want %q", code, stdout, stderr, want)
	}
	if binaryUpdateChecker() != nil {
		t.Fatal("stripped self-update still checks for a new version at startup")
	}
}
