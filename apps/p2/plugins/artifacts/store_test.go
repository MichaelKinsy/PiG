package artifacts

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// vendored is a file of Pi's apps/p2, carried verbatim under web/upstream.
func vendored(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile("../../web/upstream/" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

// The bridge and the tool's description are the text Pi's modules have (they are copied, so a pin bump that changes them
// fails here).
func TestTextsAreUpstreams(t *testing.T) {
	store := vendored(t, "plugins/artifacts/store.ts")
	_, rest, _ := strings.Cut(store, "DIAGNOSTIC_SCRIPT = `")
	script, _, _ := strings.Cut(rest, "`;")
	if script != diagnosticScript {
		t.Errorf("the diagnostics bridge differs from upstream's:\n%s\n%s", script, diagnosticScript)
	}
	description := regexp.MustCompile(`description:\s*("(?:[^"\\]|\\.)*")`).FindStringSubmatch(vendored(t, "plugins/artifacts/tool.ts"))
	if description == nil {
		t.Fatal("no description in upstream's tool.ts")
	}
	unquoted, err := strconv.Unquote(description[1])
	if err != nil || unquoted != toolDescription {
		t.Errorf("the tool's description differs from upstream's (%v):\n%s", err, unquoted)
	}
}

// What `/<head(?:\s[^>]*)?>/i` and `/<svg(?:\s[^>]*)?>/i` find in JavaScript (computed in Node), with the bridge marked "@@".
func TestInstrumentFindsTheRootTagAsJavaScriptsRegexpDoes(t *testing.T) {
	for _, test := range []struct {
		kind    p2runtime.ArtifactType
		content string
		want    string
	}{
		{p2runtime.ArtifactHTML, "<html><head><title>x</title></head>", "<html><head>@@<title>x</title></head>"},
		{p2runtime.ArtifactHTML, "<HEAD lang=en>x", "<HEAD lang=en>@@x"},
		{p2runtime.ArtifactHTML, "<header><head >y", "<header><head >@@y"},
		{p2runtime.ArtifactHTML, "<head\u00a0a>z", "<head\u00a0a>@@z"},
		{p2runtime.ArtifactHTML, "<head", "@@<head"},
		{p2runtime.ArtifactHTML, "<head a", "@@<head a"},
		{p2runtime.ArtifactHTML, "<header>", "@@<header>"},
		{p2runtime.ArtifactHTML, "<p>no head</p>", "@@<p>no head</p>"},
		{p2runtime.ArtifactSVG, "<svg xmlns='x' viewBox='0 0 1 1'><rect/></svg>", "<svg xmlns='x' viewBox='0 0 1 1'>@@<rect/></svg>"},
		{p2runtime.ArtifactSVG, "<svg/>", "<svg/>"},
		{p2runtime.ArtifactSVG, "<svg />", "<svg />"},
		{p2runtime.ArtifactSVG, "<svgfoo><svg a>", "<svgfoo><svg a>@@"},
		{p2runtime.ArtifactSVG, "<SVG\n a='1'>", "<SVG\n a='1'>@@"},
		// U+017F does not fold to `s` in a JavaScript regular expression.
		{p2runtime.ArtifactSVG, "<\u017fvg>x<svg>", "<\u017fvg>x<svg>@@"},
		{p2runtime.ArtifactMarkdown, "<head>", "<head>"},
		{p2runtime.ArtifactText, "<svg>", "<svg>"},
	} {
		info := p2runtime.ArtifactInfo{Name: "a", Title: "A", Type: test.kind, Version: 1}
		got := InstrumentArtifact(info, test.content)
		switch test.kind {
		case p2runtime.ArtifactHTML:
			got = strings.Replace(got, "<script>"+diagnosticScript+"</script>", "@@", 1)
		case p2runtime.ArtifactSVG:
			got = strings.Replace(got, "<script><![CDATA["+diagnosticScript+"]]></script>", "@@", 1)
		}
		if got != test.want {
			t.Errorf("%s %q: %q, want %q", test.kind, test.content, got, test.want)
		}
	}
}

func TestVersionOfIsNumberOfASafeIntegerOfAtLeastOne(t *testing.T) {
	for text, want := range map[string]int{"1": 1, "  2 ": 2, "0x10": 16, "1e1": 10, "0b11": 3, "0o7": 7, "+3": 3, "3.": 3} {
		if got, ok := versionOf(text); !ok || got != want {
			t.Errorf("versionOf(%q) = %d, %v; want %d", text, got, ok, want)
		}
	}
	for _, text := range []string{"", "0", "-1", "1.5", "abc", "Infinity", "1_0", "0x", "1e400", "9007199254740993", "nan", "inf"} {
		if got, ok := versionOf(text); ok {
			t.Errorf("versionOf(%q) = %d, want none", text, got)
		}
	}
}

func TestValidationRefusesWhatTheStoreRefuses(t *testing.T) {
	good := ArtifactInput{Name: "demo", Title: "Demo", Type: p2runtime.ArtifactHTML, Content: "<p>x</p>"}
	if err := validateInput(good); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]struct {
		edit   func(*ArtifactInput)
		status int
		text   string
	}{
		"empty name":          {func(i *ArtifactInput) { i.Name = "" }, 400, "Invalid artifact name"},
		"upper-case name":     {func(i *ArtifactInput) { i.Name = "Demo" }, 400, "Invalid artifact name"},
		"path name":           {func(i *ArtifactInput) { i.Name = "../demo" }, 400, "Invalid artifact name"},
		"long name":           {func(i *ArtifactInput) { i.Name = strings.Repeat("a", 65) }, 400, "Invalid artifact name"},
		"trailing newline":    {func(i *ArtifactInput) { i.Name = "demo\n" }, 400, "Invalid artifact name"},
		"empty title":         {func(i *ArtifactInput) { i.Title = "" }, 400, "Invalid artifact title (1–200 characters)"},
		"padded title":        {func(i *ArtifactInput) { i.Title = " x" }, 400, "Invalid artifact title (1–200 characters)"},
		"no-break title":      {func(i *ArtifactInput) { i.Title = "x\u00a0" }, 400, "Invalid artifact title (1–200 characters)"},
		"control title":       {func(i *ArtifactInput) { i.Title = "a\x07b" }, 400, "Invalid artifact title (1–200 characters)"},
		"line sep title":      {func(i *ArtifactInput) { i.Title = "a\u2028b" }, 400, "Invalid artifact title (1–200 characters)"},
		"long title":          {func(i *ArtifactInput) { i.Title = strings.Repeat("x", 201) }, 400, "Invalid artifact title (1–200 characters)"},
		"astral title length": {func(i *ArtifactInput) { i.Title = strings.Repeat("\U0001F600", 101) }, 400, "Invalid artifact title (1–200 characters)"},
		"unknown type":        {func(i *ArtifactInput) { i.Type = "pdf" }, 400, "Invalid artifact type"},
		"ill-formed content":  {func(i *ArtifactInput) { i.Content = "a\xffb" }, 400, "Invalid artifact content"},
		"large content":       {func(i *ArtifactInput) { i.Content = strings.Repeat("é", MaxArtifactBytes/2+1) }, 413, "Artifact content exceeds 2 MiB"},
	} {
		input := good
		change.edit(&input)
		err := validateInput(input)
		refused, ok := err.(*ArtifactError)
		if !ok || refused.Status != change.status || refused.Message != change.text {
			t.Errorf("%s: %v, want %d %q", name, err, change.status, change.text)
		}
	}
	// Exactly 100 astral characters are 200 UTF-16 units: allowed. Exactly 2 MiB is allowed.
	at := good
	at.Title = strings.Repeat("\U0001F600", 100)
	at.Content = strings.Repeat("é", MaxArtifactBytes/2)
	if err := validateInput(at); err != nil {
		t.Errorf("limits: %v", err)
	}
}

func TestPreviewStopsAtTheLimitInUTF16UnitsNeverInsideAPair(t *testing.T) {
	const limit = 512 * 1024
	if shown, cut := preview(strings.Repeat("x", limit)); cut || len(shown) != limit {
		t.Errorf("at the limit: cut=%v len=%d", cut, len(shown))
	}
	if shown, cut := preview(strings.Repeat("x", limit+1)); !cut || len(shown) != limit {
		t.Errorf("past the limit: cut=%v len=%d", cut, len(shown))
	}
	// 3-byte characters: more bytes than the limit, as many units as it: not cut.
	if shown, cut := preview(strings.Repeat("€", limit)); cut || len([]rune(shown)) != limit {
		t.Errorf("multi-byte at the limit: cut=%v", cut)
	}
	// A pair that straddles the limit is left out whole.
	content := strings.Repeat("x", limit-1) + "\U0001F600" + "tail"
	shown, cut := preview(content)
	if !cut || shown != strings.Repeat("x", limit-1) {
		t.Errorf("pair at the cut: cut=%v len=%d", cut, len(shown))
	}
}

// Ports artifacts.test.ts's last assertion: an artifact over 2 MiB is refused before the session's files are touched (the files
// given are nil: reaching them would panic).
func TestSaveRefusesALargeArtifactBeforeTouchingTheFiles(t *testing.T) {
	_, err := SaveArtifact(nil, ArtifactInput{Name: "large", Title: "Large", Type: p2runtime.ArtifactText, Content: strings.Repeat("é", MaxArtifactBytes/2+1)}, "1", context.Background())
	refused, ok := err.(*ArtifactError)
	if !ok || refused.Status != 413 || !strings.Contains(refused.Message, "2 MiB") {
		t.Fatalf("err = %v", err)
	}
}
