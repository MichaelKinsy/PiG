package runtime

import (
	"context"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/src/runtime/composer.ts: composer text expanders (p2-tui-port.md 10.8): the web composer asks them, in
// key order, for the text to send (the prompt plugin expands `/template` and `/skill:` on the session worker).

// ComposerExpander is the text to send to a session's conversation instead of text, or nil to leave it alone.
type ComposerExpander func(h host.Host, session string, conversation int, text string) (*string, error)

// ComposerExpanders is the registry of expanders (`p2.web-composer-expanders`).
var ComposerExpanders = NewKeyedRegistry[ComposerExpander]("p2.web-composer-expanders")

// ExpandComposerText is text as the first expander (in key order) that changes it gives it, else unchanged.
func ExpandComposerText(ctx context.Context, h host.Host, session string, conversation int, text string) (string, error) {
	source, err := h.Registry(ctx, ComposerExpanders.Registry, host.GetOptions{})
	if err != nil {
		return "", err
	}
	table, err := ComposerExpanders.Table(source)
	if err != nil {
		return "", err
	}
	keys := table.Keys()
	slices.SortStableFunc(keys, localeCompare)
	for _, key := range keys {
		expand, _ := table.Get(key)
		expanded, err := expand(h, session, conversation, text)
		if err != nil {
			return "", err
		}
		if expanded != nil && *expanded != text {
			return *expanded, nil
		}
	}
	return text, nil
}
