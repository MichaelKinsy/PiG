package docsdrift

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// The user-facing documentation corpus: every page a user, an extension author or an agent reads. The checks in this package run over all of it, so a page cannot drift because it sits outside the one directory a check was written for.
//
// Each entry is a path or a glob relative to the repository root. A glob that matches nothing fails the gate, so a moved directory cannot silently shrink the corpus.
var corpusGlobs = []string{
	"README.md",
	"docs/README.md",
	"docs/extensions.md",
	"docs/extension-authoring.md",
	"docs/site/docs/*.md",
	"internal/pigdocs/content/*.md",
	"extensions/sdk/README.md",
	"extensions/sdk-py/README.md",
	"extensions/sdk-rs/README.md",
	"extensions/sdk-ts/README.md",
	"piglets/README.md",
	"piglets/*/README.md",
}

const repoRoot = "../.."

type docSource struct {
	Rel  string
	Body string
}

var (
	corpusOnce  sync.Once
	corpusFiles []docSource
	corpusErr   error
)

func corpus(t *testing.T) []docSource {
	t.Helper()
	corpusOnce.Do(func() {
		seen := map[string]bool{}
		for _, glob := range corpusGlobs {
			matches, err := filepath.Glob(filepath.Join(repoRoot, filepath.FromSlash(glob)))
			if err != nil {
				corpusErr = err
				return
			}
			if len(matches) == 0 {
				corpusErr = &missingGlobError{glob}
				return
			}
			for _, m := range matches {
				rel, _ := filepath.Rel(repoRoot, m)
				rel = filepath.ToSlash(rel)
				if seen[rel] {
					continue
				}
				seen[rel] = true
				data, err := os.ReadFile(m)
				if err != nil {
					corpusErr = err
					return
				}
				corpusFiles = append(corpusFiles, docSource{Rel: rel, Body: string(data)})
			}
		}
		sort.Slice(corpusFiles, func(i, j int) bool { return corpusFiles[i].Rel < corpusFiles[j].Rel })
	})
	if corpusErr != nil {
		t.Fatalf("documentation corpus: %v", corpusErr)
	}
	return corpusFiles
}

type missingGlobError struct{ glob string }

func (e *missingGlobError) Error() string {
	return "the corpus glob " + e.glob + " matches no file; update corpusGlobs in test/docs-drift/corpus_test.go"
}

// A sample is one fenced code block. A block the gate cannot check says why in an HTML comment on the line before its fence:
//
//	<!-- sample: skip: <reason> -->
type sample struct {
	Src  string
	Line int
	Lang string
	Body string
	Skip string
}

var (
	fenceOpen = regexp.MustCompile("^(\\s*)(`{3,}|~{3,})\\s*([A-Za-z0-9_+#.-]*)")
	skipMark  = regexp.MustCompile(`^<!--\s*sample:\s*skip:\s*(.+?)\s*-->\s*$`)
)

func extractSamples(src docSource) []sample {
	var out []sample
	lines := strings.Split(src.Body, "\n")
	for i := 0; i < len(lines); i++ {
		m := fenceOpen.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		fence, indent := m[2], m[1]
		s := sample{Src: src.Rel, Line: i + 1, Lang: strings.ToLower(m[3])}
		if i > 0 {
			if sm := skipMark.FindStringSubmatch(strings.TrimSpace(lines[i-1])); sm != nil {
				s.Skip = sm[1]
			}
		}
		var body []string
		j := i + 1
		for ; j < len(lines); j++ {
			if closesFence(lines[j], fence) {
				break
			}
			body = append(body, strings.TrimPrefix(lines[j], indent))
		}
		s.Body = strings.Join(body, "\n")
		out = append(out, s)
		i = j
	}
	return out
}

func allSamples(t *testing.T) []sample {
	t.Helper()
	var out []sample
	for _, src := range corpus(t) {
		out = append(out, extractSamples(src)...)
	}
	if len(out) < 100 {
		t.Fatalf("extracted only %d code samples; the extractor is broken", len(out))
	}
	return out
}

func TestCorpusExtractsFencedSamples(t *testing.T) {
	src := docSource{Rel: "x.md", Body: "<!-- sample: skip: why -->\n```json\n{\"a\":1}\n```\ntext\n````md\n```inner```\n````\n  ```go\n  fmt.Println()\n  ```\n"}
	got := extractSamples(src)
	if len(got) != 3 {
		t.Fatalf("samples = %+v", got)
	}
	if got[0].Lang != "json" || got[0].Skip != "why" || got[0].Body != "{\"a\":1}" || got[0].Line != 2 {
		t.Errorf("first sample = %+v", got[0])
	}
	if got[1].Lang != "md" || !strings.Contains(got[1].Body, "inner") {
		t.Errorf("a longer fence must hold a shorter one: %+v", got[1])
	}
	if got[2].Lang != "go" || got[2].Body != "fmt.Println()" {
		t.Errorf("an indented fence loses its indent: %+v", got[2])
	}
}

// closesFence reports whether line closes a fence of at least len(fence) of the same character and nothing else.
func closesFence(line, fence string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= len(fence) && strings.Trim(t, fence[:1]) == ""
}
