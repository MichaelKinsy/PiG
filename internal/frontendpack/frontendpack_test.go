package frontendpack

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

type member struct{ id int }

func (member) Open(frontend.Env) (frontend.Session, error) { return nil, nil }

// Stock PiG fuses no member, so the interactive mode keeps its ANSI renderer;
// a Piglet Binary's generated registration makes Selected construct the
// member.
func TestSelectedReturnsTheRegisteredMemberOrNil(t *testing.T) {
	t.Cleanup(func() { selected = nil })
	if got := Selected(); got != nil {
		t.Fatalf("stock Selected() = %v", got)
	}
	register(func() frontend.Frontend { return member{id: 7} })
	if got, ok := Selected().(member); !ok || got.id != 7 {
		t.Fatalf("Selected() = %#v", got)
	}
}
