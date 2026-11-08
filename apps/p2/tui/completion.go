package tui

// The chat editor's completion (p2-tui-port.md section 8.4, P7b): pi-tui's CombinedAutocompleteProvider behavior over p2 sources. `/` goes to the command registry's
// slash completion; `@prefix` asks the session's FileIndex (ranked like pi's `@`); a forced Tab on a path-like token lists that directory (`FileIndex.list`) and
// filters it locally. Never pi-tui's own provider: it reads the local filesystem and spawns fd. Requests are aborted when superseded (the editor's context) and
// debounced by the editor for `@`.
//
// Go forced difference: the editor runs a provider's synchronous part on its owner loop and awaited work on a worker, so the file index's calls are in the
// task SuggestionTask returns (a pitui.AsyncSuggestionPlanner) and GetSuggestions answers the branches without a call.
//
// pi: apps/p2/src/tui/completion.ts

import (
	"context"
	"regexp"
	"strings"
	"sync"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// FileSource is what the provider needs from a session's FileIndex (a live `FileIndex` reader reference satisfies it): its calls block.
type FileSource interface {
	Complete(ctx context.Context, query string, limit int) (items []p2runtime.IndexedPath, truncated bool, err error)
	List(ctx context.Context, path string) (entries []p2runtime.DirectoryEntry, truncated bool, err error)
}

// OutOfScopeHint and PartialHint are the status lines of a completion the index cannot give.
const (
	OutOfScopeHint = "File completion covers the session directory only"
	PartialHint    = "File index is partial (the directory is very large)"
)

// FileCompletionProvider is the chat editor's autocomplete provider.
type FileCompletionProvider struct {
	slash pitui.AutocompleteProvider
	files func() FileSource
	hint  func(text string)
	// mentions is the agent's name while chat mode is on (its `@name` is offered first), else "".
	mentions func() string

	mu sync.Mutex
	// lastKind is which source answered the last request: its prefix kind decides how a chosen item is inserted.
	lastKind string
}

var (
	_ pitui.AutocompleteProvider   = (*FileCompletionProvider)(nil)
	_ pitui.AsyncSuggestionPlanner = (*FileCompletionProvider)(nil)
)

// NewFileCompletionProvider is the provider over the slash completion and the file source (nil while the index is not live). hint shows a status line; it is
// called from any goroutine. mentions is nil without chat mode.
func NewFileCompletionProvider(slash pitui.AutocompleteProvider, files func() FileSource, hint func(text string), mentions func() string) *FileCompletionProvider {
	if mentions == nil {
		mentions = func() string { return "" }
	}
	return &FileCompletionProvider{slash: slash, files: files, hint: hint, mentions: mentions, lastKind: "file"}
}

// TriggerCharacters are the characters that open completion by themselves.
func (p *FileCompletionProvider) TriggerCharacters() []string { return []string{"@"} }

func (p *FileCompletionProvider) setKind(kind string) {
	p.mu.Lock()
	p.lastKind = kind
	p.mu.Unlock()
}

func (p *FileCompletionProvider) kind() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastKind
}

var slashArgument = regexp.MustCompile(`^\s*/\S+\s`)

func items(of []p2runtime.CompletionItem) []pitui.AutocompleteItem {
	out := make([]pitui.AutocompleteItem, len(of))
	for i, item := range of {
		out[i] = pitui.AutocompleteItem{Value: item.Value, Label: item.Label, Description: item.Description}
	}
	return out
}

// request is what a call asks for: an answer at once, or a task the source's calls run in.
type request struct {
	result *pitui.AutocompleteSuggestions
	prefix string
	task   func(ctx context.Context) []pitui.AutocompleteItem
}

func (p *FileCompletionProvider) resolve(ctx context.Context, lines []string, row, col int, force bool) request {
	if row < 0 || row >= len(lines) {
		return request{}
	}
	line := lines[row]
	before := line[:min(max(col, 0), len(line))]
	if at, ok := p2runtime.ExtractAtPrefix(before); ok {
		p.setKind("file")
		parsed := p2runtime.ParsePathPrefix(at)
		// `@pi` is chat mode's mention, never a file; in chat mode it is offered first (section 8.4).
		name := p.mentions()
		mentionName := name
		if mentionName == "" {
			mentionName = p2runtime.DefaultMentionName
		}
		if p2runtime.IsMentionToken(at, mentionName) {
			return request{}
		}
		mention := items(p2runtime.MentionItems(parsed.RawPrefix, name))
		if p2runtime.OutOfScope(parsed.RawPrefix) {
			p.hint(OutOfScopeHint)
			if len(mention) > 0 {
				return request{result: &pitui.AutocompleteSuggestions{Items: mention, Prefix: at}}
			}
			return request{}
		}
		return request{prefix: at, task: func(ctx context.Context) []pitui.AutocompleteItem {
			var found []p2runtime.IndexedPath
			truncated := false
			if files := p.files(); files != nil {
				if got, partial, err := files.Complete(ctx, parsed.RawPrefix, 20); err == nil {
					found, truncated = got, partial
				}
			}
			if ctx.Err() != nil {
				return nil
			}
			if truncated {
				p.hint(PartialHint)
			}
			return append(mention, items(p2runtime.AtItems(found, parsed.IsQuotedPrefix))...)
		}}
	}
	if !force && strings.HasPrefix(strings.TrimLeft(before, " \t\n\r\f\v"), "/") {
		p.setKind("slash")
		return request{result: p.slash.GetSuggestions(ctx, lines, row, col, pitui.AutocompleteSuggestionOptions{})}
	}
	if !force {
		return request{}
	}
	// Tab in a command's argument (`/cd sr`): the command's own completion first (pi's argument completions), then files as anywhere else.
	if slashArgument.MatchString(before) {
		if own := p.slash.GetSuggestions(ctx, lines, row, col, pitui.AutocompleteSuggestionOptions{Force: true}); own != nil && len(own.Items) > 0 {
			p.setKind("slash")
			return request{result: own}
		}
	}
	prefix, ok := p2runtime.ExtractPathPrefix(before, true)
	if !ok {
		return request{}
	}
	p.setKind("file")
	parsed := p2runtime.ParsePathPrefix(prefix)
	if p2runtime.OutOfScope(parsed.RawPrefix) {
		p.hint(OutOfScopeHint)
		return request{}
	}
	return request{prefix: prefix, task: func(ctx context.Context) []pitui.AutocompleteItem {
		files := p.files()
		if files == nil {
			return nil
		}
		entries, _, err := files.List(ctx, p2runtime.PathCompletion(parsed.RawPrefix).Dir)
		if err != nil || ctx.Err() != nil {
			return nil
		}
		return items(p2runtime.PathItems(entries, parsed.RawPrefix, parsed.IsAtPrefix, parsed.IsQuotedPrefix))
	}}
}

// GetSuggestions answers what needs no call to the file index.
func (p *FileCompletionProvider) GetSuggestions(ctx context.Context, lines []string, row, col int, options pitui.AutocompleteSuggestionOptions) *pitui.AutocompleteSuggestions {
	return p.resolve(ctx, lines, row, col, options.Force).result
}

// SuggestionTask is the part that calls the file index, off the editor's loop.
func (p *FileCompletionProvider) SuggestionTask(lines []string, row, col int, force bool) (string, func(context.Context) ([]pitui.AutocompleteItem, error), bool) {
	request := p.resolve(context.Background(), lines, row, col, force)
	if request.task == nil {
		return "", nil, false
	}
	return request.prefix, func(ctx context.Context) ([]pitui.AutocompleteItem, error) {
		found := request.task(ctx)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return found, nil
	}, true
}

// ApplyCompletion inserts the chosen item the way its source's prefix needs.
func (p *FileCompletionProvider) ApplyCompletion(lines []string, row, col int, item pitui.AutocompleteItem, prefix string) ([]string, int, int) {
	if p.kind() == "slash" {
		return p.slash.ApplyCompletion(lines, row, col, item, prefix)
	}
	if row < 0 || row >= len(lines) {
		return lines, row, col
	}
	line, cursor := p2runtime.ApplyFileCompletion(lines[row], col, p2runtime.CompletionItem{Value: item.Value, Label: item.Label, Description: item.Description}, prefix)
	next := append([]string(nil), lines...)
	next[row] = line
	return next, row, cursor
}

// ShouldTriggerFileCompletion is whether a forced Tab completes files here.
func (p *FileCompletionProvider) ShouldTriggerFileCompletion(lines []string, row, col int) bool {
	if row < 0 || row >= len(lines) {
		return false
	}
	line := lines[row]
	return p2runtime.ShouldTriggerFileCompletion(line[:min(max(col, 0), len(line))])
}
