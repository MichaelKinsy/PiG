package artifacts

import (
	"context"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/artifacts/store.ts: an artifact's validation, its versions in the session's files (space
// `artifacts`, path = its name, Files versions = artifact versions, never reused) and the preview's diagnostics bridge.

// MaxArtifactBytes is the largest artifact content: 2 MiB of UTF-8.
const MaxArtifactBytes = 2 * 1024 * 1024

var artifactName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ArtifactInput is an artifact version to save.
type ArtifactInput struct {
	Name    string
	Title   string
	Type    p2runtime.ArtifactType
	Content string
}

// ArtifactError is a refused artifact, with the HTTP status the route answers.
type ArtifactError struct {
	Status  int
	Message string
}

func (err *ArtifactError) Error() string { return err.Message }

// openTag is the first tag `<name` (any case) of content that is closed by `>` and has attributes only after white space:
// the start of the tag and the index after its `>`, as `/<name(?:\s[^>]*)?>/i` finds it. The name is matched on ASCII
// letters only (JavaScript's case folding keeps U+017F and U+212A from matching `s` and `k`).
func openTag(content, name string) (start, end int, found bool) {
	for from := 0; ; {
		index := indexFoldASCII(content[from:], "<"+name)
		if index < 0 {
			return 0, 0, false
		}
		start = from + index
		rest := start + 1 + len(name)
		if rest < len(content) && content[rest] == '>' {
			return start, rest + 1, true
		}
		if rest < len(content) {
			if r, _ := utf8.DecodeRuneInString(content[rest:]); isJSSpace(r) {
				if closing := strings.IndexByte(content[rest:], '>'); closing >= 0 {
					return start, rest + closing + 1, true
				}
			}
		}
		from = start + 1
	}
}

// indexFoldASCII is the index of the first occurrence of needle (lower-case ASCII) in text, ASCII letters compared
// without case.
func indexFoldASCII(text, needle string) int {
	for i := 0; i+len(needle) <= len(text); i++ {
		matched := true
		for j := 0; j < len(needle); j++ {
			c := text[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != needle[j] {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

// isJSSpace is JavaScript's `\s` and what `String.prototype.trim` removes.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// InstrumentArtifact adds a diagnostics bridge without changing the immutable stored source.
func InstrumentArtifact(info p2runtime.ArtifactInfo, content string) string {
	switch info.Type {
	case p2runtime.ArtifactHTML:
		script := "<script>" + diagnosticScript + "</script>"
		if _, end, found := openTag(content, "head"); found {
			return content[:end] + script + content[end:]
		}
		return script + content
	case p2runtime.ArtifactSVG:
		// SVG is XML: the script's `&&` would end the document without CDATA. An empty root (`<svg/>`) has no inside.
		script := "<script><![CDATA[" + diagnosticScript + "]]></script>"
		if _, end, found := openTag(content, "svg"); found && !strings.HasSuffix(content[:end], "/>") {
			return content[:end] + script + content[end:]
		}
	}
	return content
}

func validTitle(title string) bool {
	if title == "" || !utf8.ValidString(title) || len(utf16.Encode([]rune(title))) > 200 || strings.TrimFunc(title, isJSSpace) != title {
		return false
	}
	for _, r := range title {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}

func validType(artifactType p2runtime.ArtifactType) bool {
	switch artifactType {
	case p2runtime.ArtifactHTML, p2runtime.ArtifactSVG, p2runtime.ArtifactMarkdown, p2runtime.ArtifactText:
		return true
	}
	return false
}

func validName(name string) bool {
	return artifactName.MatchString(name) && strings.TrimFunc(name, isJSSpace) == name
}

func validateInput(input ArtifactInput) error {
	if !validName(input.Name) {
		return &ArtifactError{400, "Invalid artifact name"}
	}
	if !validTitle(input.Title) {
		return &ArtifactError{400, "Invalid artifact title (1–200 characters)"}
	}
	if !validType(input.Type) {
		return &ArtifactError{400, "Invalid artifact type"}
	}
	if !utf8.ValidString(input.Content) {
		return &ArtifactError{400, "Invalid artifact content"}
	}
	if len(input.Content) > MaxArtifactBytes {
		return &ArtifactError{413, "Artifact content exceeds 2 MiB"}
	}
	return nil
}

// mimeOf is the type an artifact's bytes are stored with.
var mimeOf = map[p2runtime.ArtifactType]string{
	p2runtime.ArtifactHTML:     "text/html",
	p2runtime.ArtifactSVG:      "image/svg+xml",
	p2runtime.ArtifactMarkdown: "text/markdown",
	p2runtime.ArtifactText:     "text/plain",
}

// SaveArtifact saves input as the next version of its artifact in the session's files (space `artifacts`, path = its
// name), written by the agent of conversation ref. Locators do not authorize access; callers must authorize the session.
func SaveArtifact(files *host.Reference, input ArtifactInput, ref string, ctx context.Context) (p2runtime.ArtifactInfo, error) {
	if err := validateInput(input); err != nil {
		return p2runtime.ArtifactInfo{}, err
	}
	file, err := files.Call("put", delta.JsonObjectOf(
		"space", p2runtime.ArtifactsSpace, "path", input.Name, "mime", mimeOf[input.Type], "source", "agent", "ref", ref,
		"meta", delta.JsonObjectOf("title", input.Title, "type", string(input.Type)), "bytes", []byte(input.Content),
	), ctx)
	if err != nil {
		return p2runtime.ArtifactInfo{}, err
	}
	version, _ := file.(*delta.JsonObject).Value("version").(float64)
	return p2runtime.ArtifactInfo{Name: input.Name, Title: input.Title, Type: input.Type, Version: int(version)}, nil
}

var errNotFound = &ArtifactError{404, "Artifact not found"}

// ReadArtifact is version `version` of artifact `name` of the session's files; a missing or invalid one is "Artifact not
// found".
func ReadArtifact(files *host.Reference, name string, version int, ctx context.Context) (p2runtime.ArtifactInfo, string, error) {
	if !validName(name) || version < 1 {
		return p2runtime.ArtifactInfo{}, "", errNotFound
	}
	found, err := files.Call("get", delta.JsonObjectOf("space", p2runtime.ArtifactsSpace, "path", name, "version", float64(version)), ctx)
	if err != nil {
		return p2runtime.ArtifactInfo{}, "", err
	}
	file, _ := found.(*delta.JsonObject)
	if file == nil || file.Value("size").(float64) > MaxArtifactBytes {
		return p2runtime.ArtifactInfo{}, "", errNotFound
	}
	bytes, err := p2runtime.ReadWhole(files, file, ctx)
	if err != nil {
		return p2runtime.ArtifactInfo{}, "", err
	}
	// Bytes that are not UTF-8 would read back changed.
	if !utf8.Valid(bytes) {
		return p2runtime.ArtifactInfo{}, "", errNotFound
	}
	meta, _ := file.Value("meta").(*delta.JsonObject)
	title, _ := meta.Value("title").(string)
	artifactType, _ := meta.Value("type").(string)
	input := ArtifactInput{Name: name, Title: title, Type: p2runtime.ArtifactType(artifactType), Content: string(bytes)}
	if meta == nil || validateInput(input) != nil {
		return p2runtime.ArtifactInfo{}, "", errNotFound
	}
	return p2runtime.ArtifactInfo{Name: name, Title: title, Type: input.Type, Version: version}, input.Content, nil
}
