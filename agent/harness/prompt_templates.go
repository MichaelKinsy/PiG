// Ports packages/agent/src/harness/prompt-templates.ts.
package harness

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/internal/codingagent/frontmatter"
)

// PromptTemplateDiagnostic is a warning from the environment or Markdown parser.
type PromptTemplateDiagnostic struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Path    string `json:"path"`
}

// SourcedPath associates an application-owned provenance value with an input path.
type SourcedPath[S any] struct {
	Path   string `json:"path"`
	Source S      `json:"source"`
}

// SourcedPromptTemplate retains the mapped template and its unchanged provenance. Without a mapper, PromptTemplate contains a harness.PromptTemplate; a mapper may supply an application-defined template value.
type SourcedPromptTemplate[S any] struct {
	PromptTemplate any `json:"promptTemplate"`
	Source         S   `json:"source"`
}

// SourcedPromptTemplateDiagnostic retains the warning and its input provenance.
type SourcedPromptTemplateDiagnostic[S any] struct {
	PromptTemplateDiagnostic
	Source S `json:"source"`
}

// LoadPromptTemplates reads explicit Markdown files and direct Markdown children of directories. It waits for each environment operation and reports read/parse failures without discarding valid siblings.
func LoadPromptTemplates(ctx context.Context, env ExecutionEnv, paths []string) ([]PromptTemplate, []PromptTemplateDiagnostic) {
	templates := []PromptTemplate{}
	diagnostics := []PromptTemplateDiagnostic{}
	for _, path := range paths {
		info, err := env.FileInfo(ctx, path)
		if err != nil {
			appendResourceInfoError(&diagnostics, path, err)
			continue
		}
		kind := resolveResourceKind(ctx, env, info, &diagnostics)
		if kind == FileKindDirectory {
			entries, listErr := env.ListDir(ctx, info.Path)
			if listErr != nil {
				diagnostics = append(diagnostics, resourceWarning("list_failed", info.Path, listErr.Error()))
				continue
			}
			sortResourceEntries(entries)
			for _, entry := range entries {
				if resolveResourceKind(ctx, env, entry, &diagnostics) == FileKindFile && strings.HasSuffix(entry.Name, ".md") {
					template, warning := loadPromptTemplateFile(ctx, env, entry)
					if template != nil {
						templates = append(templates, *template)
					}
					diagnostics = append(diagnostics, warning...)
				}
			}
		} else if kind == FileKindFile && strings.HasSuffix(info.Name, ".md") {
			template, warning := loadPromptTemplateFile(ctx, env, info)
			if template != nil {
				templates = append(templates, *template)
			}
			diagnostics = append(diagnostics, warning...)
		}
	}
	return templates, diagnostics
}

// LoadSourcedPromptTemplates invokes mapper in load order and attaches each input's source to its templates and warnings. A nil mapper retains the base PromptTemplate. Mapping runs synchronously with the same caller context; a mapper error stops loading and is returned unchanged.
func LoadSourcedPromptTemplates[S any](ctx context.Context, env ExecutionEnv, inputs []SourcedPath[S], mapper func(PromptTemplate, S, context.Context) (any, error)) ([]SourcedPromptTemplate[S], []SourcedPromptTemplateDiagnostic[S], error) {
	templates := []SourcedPromptTemplate[S]{}
	diagnostics := []SourcedPromptTemplateDiagnostic[S]{}
	for _, input := range inputs {
		loaded, warnings := LoadPromptTemplates(ctx, env, []string{input.Path})
		for _, template := range loaded {
			var value any = template
			if mapper != nil {
				var err error
				value, err = mapper(template, input.Source, ctx)
				if err != nil {
					return nil, nil, err
				}
			}
			templates = append(templates, SourcedPromptTemplate[S]{PromptTemplate: value, Source: input.Source})
		}
		for _, warning := range warnings {
			diagnostics = append(diagnostics, SourcedPromptTemplateDiagnostic[S]{PromptTemplateDiagnostic: warning, Source: input.Source})
		}
	}
	return templates, diagnostics, nil
}

func loadPromptTemplateFile(ctx context.Context, env ExecutionEnv, info FileInfo) (*PromptTemplate, []PromptTemplateDiagnostic) {
	content, err := env.ReadTextFile(ctx, info.Path)
	if err != nil {
		return nil, []PromptTemplateDiagnostic{resourceWarning("read_failed", info.Path, err.Error())}
	}
	fields, body, err := parseResourceFrontmatter(content)
	if err != nil {
		return nil, []PromptTemplateDiagnostic{resourceWarning("parse_failed", info.Path, err.Error())}
	}
	description, _ := fields["description"].(string)
	if description == "" {
		for line := range strings.SplitSeq(body, "\n") {
			if trimResourceSpace(line) == "" {
				continue
			}
			units := utf16.Encode([]rune(line))
			description = string(utf16.Decode(units[:min(len(units), 60)]))
			if len(units) > 60 {
				description += "..."
			}
			break
		}
	}
	return &PromptTemplate{Name: strings.TrimSuffix(info.Name, ".md"), Description: description, Content: body}, nil
}

func resourceWarning(code, path, message string) PromptTemplateDiagnostic {
	return PromptTemplateDiagnostic{Type: "warning", Code: code, Message: message, Path: path}
}
func appendResourceInfoError(diagnostics *[]PromptTemplateDiagnostic, path string, err error) {
	if fileErr, ok := errors.AsType[*FileError](err); ok && fileErr.Code == FileErrorNotFound {
		return
	}
	*diagnostics = append(*diagnostics, resourceWarning("file_info_failed", path, err.Error()))
}
func resolveResourceKind(ctx context.Context, env ExecutionEnv, info FileInfo, diagnostics *[]PromptTemplateDiagnostic) FileKind {
	if info.Kind == FileKindFile || info.Kind == FileKindDirectory {
		return info.Kind
	}
	canonical, err := env.CanonicalPath(ctx, info.Path)
	if err != nil {
		appendResourceInfoError(diagnostics, info.Path, err)
		return ""
	}
	target, err := env.FileInfo(ctx, canonical)
	if err != nil {
		appendResourceInfoError(diagnostics, info.Path, err)
		return ""
	}
	if target.Kind == FileKindFile || target.Kind == FileKindDirectory {
		return target.Kind
	}
	return ""
}
func sortResourceEntries(entries []FileInfo) {
	comparator := collate.New(language.English)
	slices.SortStableFunc(entries, func(a, b FileInfo) int { return comparator.CompareString(a.Name, b.Name) })
}
func parseResourceFrontmatter(content string) (map[string]any, string, error) {
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(content)
	if !strings.HasPrefix(normalized, "---") {
		return map[string]any{}, normalized, nil
	}
	end := strings.Index(normalized[3:], "\n---")
	if end < 0 {
		return map[string]any{}, normalized, nil
	}
	doc := frontmatter.Parse(normalized)
	return doc.Frontmatter, trimResourceSpace(normalized[end+7:]), doc.Err
}
func isResourceSpace(r rune) bool       { return r == '\ufeff' || (r != '\u0085' && unicode.IsSpace(r)) }
func trimResourceSpace(s string) string { return strings.TrimFunc(s, isResourceSpace) }

// ParseCommandArgs splits harness command arguments on spaces and tabs outside quotes. Unlike the coding-agent parser, unquoted newlines are ordinary characters in this upstream harness API.
func ParseCommandArgs(input string) []string {
	args := []string{}
	var current strings.Builder
	var quote rune
	for _, r := range input {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

var harnessPositional = lazyregexp.New(`\$(\d+)`)
var harnessSlice = lazyregexp.New(`\$\{@:(\d+)(?::(\d+))?\}`)

// SubstituteArgs performs the harness's ordered positional, slice, ARGUMENTS and @ replacement passes. Inserted values participate in later passes, matching the harness rather than coding-agent's single-pass helper.
func SubstituteArgs(content string, args []string) string {
	result := harnessPositional.ReplaceAllStringFunc(content, func(match string) string {
		n, err := strconv.Atoi(match[1:])
		if err != nil || n < 1 || n > len(args) {
			return ""
		}
		return args[n-1]
	})
	result = harnessSlice.ReplaceAllStringFunc(result, func(match string) string {
		fields := harnessSlice.FindStringSubmatch(match)
		start, err := strconv.Atoi(fields[1])
		if err != nil {
			return ""
		}
		start = max(0, start-1)
		start = min(start, len(args))
		end := len(args)
		if fields[2] != "" {
			length, err := strconv.Atoi(fields[2])
			if err == nil {
				end = start + min(length, len(args)-start)
			}
		}
		return strings.Join(args[start:end], " ")
	})
	all := strings.Join(args, " ")
	// JavaScript's string replacement expands $$, $& and prefix/suffix tokens in replacement strings.
	result = replaceHarnessString(result, "$ARGUMENTS", all)
	return replaceHarnessString(result, "$@", all)
}
func replaceHarnessString(input, pattern, replacement string) string {
	var out strings.Builder
	offset := 0
	for {
		index := strings.Index(input[offset:], pattern)
		if index < 0 {
			out.WriteString(input[offset:])
			break
		}
		index += offset
		out.WriteString(input[offset:index])
		for i := 0; i < len(replacement); i++ {
			if replacement[i] == '$' && i+1 < len(replacement) {
				switch replacement[i+1] {
				case '$':
					out.WriteByte('$')
					i++
					continue
				case '&':
					out.WriteString(pattern)
					i++
					continue
				case '`':
					out.WriteString(input[:index])
					i++
					continue
				case '\'':
					out.WriteString(input[index+len(pattern):])
					i++
					continue
				}
			}
			out.WriteByte(replacement[i])
		}
		offset = index + len(pattern)
	}
	return out.String()
}

// FormatPromptTemplateInvocation expands a template with already parsed arguments.
func FormatPromptTemplateInvocation(template PromptTemplate, args []string) string {
	return SubstituteArgs(template.Content, args)
}
