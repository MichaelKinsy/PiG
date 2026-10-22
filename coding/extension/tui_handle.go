package extension

import (
	"errors"
	"fmt"
	"strings"
)

// pig additive (D192): TUIDriver is a UI that draws a TUI: the two calls of Pi's TUI an extension's widget factory
// reaches through the `tui` it is handed. Invalidate is TUI.invalidate (drops every component's
// render cache, Markdown transform results included); RequestRender is TUI.requestRender (schedules a
// repaint, a full one when force is set). A UI that draws none (print, JSON, RPC) does not implement
// it, and the calls are ignored, as an extension's factories never run there.
type TUIDriver interface {
	InvalidateTUI()
	RequestTUIRender(force bool)
}

// LinkSchemeRegistrar is a UI that lets an extension claim the scheme of an OSC 8 hyperlink: a click
// on a link whose address has the scheme goes to the extension's handler and nothing is opened.
// pig additive (D193).
type LinkSchemeRegistrar interface {
	// RegisterLinkScheme claims scheme for owner. It fails when another owner holds the scheme or the
	// scheme is one a link must keep opening. An owner claiming a scheme again replaces its handler.
	// The returned release gives the scheme back and is safe to call more than once.
	RegisterLinkScheme(owner, scheme string, handler func(url string)) (release func(), err error)
}

// ErrLinkSchemeInUse is returned when another extension holds the scheme.
var ErrLinkSchemeInUse = errors.New("link scheme already claimed by another extension")

// reservedLinkSchemes are the schemes the host keeps opening: an extension cannot take the clicks on
// a web, file or mail link away from the user.
var reservedLinkSchemes = map[string]bool{"http": true, "https": true, "file": true, "mailto": true}

// ValidateLinkScheme checks a scheme an extension asks to claim: an RFC 3986 scheme in lower case,
// not one the host keeps opening.
func ValidateLinkScheme(scheme string) error {
	if scheme == "" {
		return errors.New("link scheme is empty")
	}
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		switch {
		case c >= 'a' && c <= 'z':
		case i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return fmt.Errorf("link scheme %q must be lower case letters, digits, +, - or ., starting with a letter", scheme)
		}
	}
	if reservedLinkSchemes[scheme] {
		return fmt.Errorf("link scheme %q is reserved: the host keeps opening it", scheme)
	}
	return nil
}

// LinkScheme returns the scheme of an OSC 8 address in lower case, "" when it has none.
func LinkScheme(url string) string {
	at := strings.IndexByte(url, ':')
	if at <= 0 {
		return ""
	}
	return strings.ToLower(url[:at])
}
