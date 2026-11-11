package app

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// bearerRequest is a request carrying only its headers: what the sessions' authenticator reads of a bearer token.
type bearerRequest struct {
	chordhttp.HttpRequest
	header http.Header
}

func (r bearerRequest) Headers() http.Header { return r.header }

// authenticateWith asks the server's authenticators about token on ctx, as the HTTP component does for a request whose context is ctx.
func authenticateWith(t *testing.T, s *kitServer, ctx context.Context, token string) data.Principal {
	t.Helper()
	source, err := s.host.Registry(background, registries.Registry[any, any](chordhttp.Authenticators), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var entries []chordhttp.AuthenticatorEntry
	source.Batch().Section(func() { entries = source.Value().([]chordhttp.AuthenticatorEntry) })
	request := bearerRequest{header: http.Header{"Authorization": {"Bearer " + token}}}
	for _, entry := range entries {
		principal, err := entry.Authenticate(ctx, request)
		if err != nil {
			t.Fatal(err)
		}
		if principal != nil {
			return principal
		}
	}
	return nil
}

func lastUsed(t *testing.T, s *kitServer, id string) float64 {
	t.Helper()
	found := session(t, s, id)
	if found == nil {
		t.Fatalf("session %s is not listed", id)
	}
	return found.(*delta.JsonObject).Value("lastUsed").(float64)
}

// Pi's authentication does not await the touch of `lastUsed`: `void document.change(fn, context).catch(() => {})` on the request's
// context, so a request that goes away before the write took the line cancels it (the touch is dropped, a later request makes it
// again). The write is tied to the request's cancel, as Pi's.
func TestASessionsTouchIsDroppedWhenItsRequestWasCancelled(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	id, token, _ := s.issue(delta.JsonObjectOf("kind", "owner"), "b", "browser")
	before := lastUsed(t, s, id)
	_, restore := jsclock.MockDateNow(jsclock.DateNow() + 2*60_000)
	defer restore()
	gone, cancel := context.WithCancelCause(background)
	cancel(errors.New("http: the client disconnected"))
	if principal := authenticateWith(t, s, gone, token); principal == nil {
		t.Fatal("the token was refused: authentication does not wait for the touch")
	}
	sessionWrites.Wait()
	if after := lastUsed(t, s, id); after != before {
		t.Fatalf("lastUsed moved %v -> %v: the touch of a cancelled request was written, want it dropped", before, after)
	}
}

// Pi's request AbortController is aborted by a disconnect or by close, never for a request that finished: the touch of a request
// that answered at once still lands. (Go ends the request's context with ErrRequestFinished then: that end is no abort.)
func TestASessionsTouchOfAFinishedRequestStillLands(t *testing.T) {
	s := newKitServer(t, kitServerOptions{})
	defer s.close()
	id, token, _ := s.issue(delta.JsonObjectOf("kind", "owner"), "b", "browser")
	before := lastUsed(t, s, id)
	_, restore := jsclock.MockDateNow(jsclock.DateNow() + 2*60_000)
	defer restore()
	finished, cancel := context.WithCancelCause(background)
	if principal := authenticateWith(t, s, finished, token); principal == nil {
		t.Fatal("the token was refused")
	}
	cancel(chordhttp.ErrRequestFinished)
	sessionWrites.Wait()
	if after := lastUsed(t, s, id); after <= before {
		t.Fatalf("lastUsed %v -> %v: the touch of a finished request was dropped", before, after)
	}
}
