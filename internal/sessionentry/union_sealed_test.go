package sessionentry_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// foreignEntry embeds SessionEntryBase and has Raw and MarshalJSON, as a type in another package could.
type foreignEntry struct{ sessionentry.SessionEntryBase }

func (foreignEntry) Raw() json.RawMessage         { return nil }
func (foreignEntry) MarshalJSON() ([]byte, error) { return nil, nil }

// The union is closed: embedding SessionEntryBase and writing Raw and MarshalJSON does not make a type a FileEntry or a SessionEntry,
// because the marker method belongs to each member (session-manager.ts SessionEntry is a closed union of named types).
func TestTheUnionIsSealed(t *testing.T) {
	if _, ok := any(foreignEntry{}).(sessionentry.SessionEntry); ok {
		t.Error("a type outside the package that embeds SessionEntryBase is a SessionEntry")
	}
	if _, ok := any(foreignEntry{}).(sessionentry.FileEntry); ok {
		t.Error("a type outside the package that embeds SessionEntryBase is a FileEntry")
	}
	if _, ok := any(sessionentry.MessageEntry{}).(sessionentry.SessionEntry); !ok {
		t.Error("MessageEntry is no SessionEntry")
	}
}
