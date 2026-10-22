package subprocess

import (
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// linkUI is a UI that takes OSC 8 link claims. The schemes it holds are shared by its owners, as the interactive mode's are.
type linkUI struct {
	*mockUIContext
	events   []string
	handlers map[string]func(string)
	owners   map[string]string
}

func newLinkUI() *linkUI {
	return &linkUI{mockUIContext: &mockUIContext{}, handlers: map[string]func(string){}, owners: map[string]string{}}
}

func (u *linkUI) RegisterLinkScheme(owner, scheme string, handler func(string)) (func(), error) {
	if held, ok := u.owners[scheme]; ok && held != owner {
		return nil, errors.Join(extension.ErrLinkSchemeInUse, errors.New(scheme+" is held by "+held))
	}
	u.events = append(u.events, "claim "+owner+"/"+scheme)
	u.owners[scheme], u.handlers[scheme] = owner, handler
	return func() {
		u.events = append(u.events, "release "+owner+"/"+scheme)
		delete(u.owners, scheme)
		delete(u.handlers, scheme)
	}, nil
}

func claim(t *testing.T, bridge *UIBridge, ext, scheme string) *CallResultPayload {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"scheme": scheme})
	result, err := bridge.HandleCall(ext, &CallPayload{Method: CallUIOnLinkClick, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUIBridgeLinkClaimReachesTheUI(t *testing.T) {
	ui := newLinkUI()
	bridge := newTestBridge(ui)
	if result := claim(t, bridge, "ext-a", "stretchtext"); result.Error != nil {
		t.Fatalf("claim error = %+v", result.Error)
	}
	if want := []string{"claim ext-a/stretchtext"}; !slices.Equal(ui.events, want) {
		t.Fatalf("events = %q, want %q", ui.events, want)
	}
	off, _ := json.Marshal(map[string]string{"scheme": "stretchtext"})
	if result, err := bridge.HandleCall("ext-a", &CallPayload{Method: CallUIOffLinkClick, Args: off}); err != nil || result.Error != nil {
		t.Fatalf("off = %+v, %v", result, err)
	}
	if want := []string{"claim ext-a/stretchtext", "release ext-a/stretchtext"}; !slices.Equal(ui.events, want) {
		t.Fatalf("events = %q, want %q", ui.events, want)
	}
}

func TestUIBridgeLinkClaimRefusesWhatTheHostKeepsOpening(t *testing.T) {
	ui := newLinkUI()
	bridge := newTestBridge(ui)
	for _, scheme := range []string{"http", "https", "file", "mailto", "", "Stretch", "1abc", "a b", "a_b"} {
		result := claim(t, bridge, "ext-a", scheme)
		if result.Error == nil || result.Error.Code != "invalid_scheme" {
			t.Fatalf("claim of %q = %+v, want invalid_scheme", scheme, result.Error)
		}
	}
	if len(ui.events) != 0 {
		t.Fatalf("a refused scheme reached the UI: %q", ui.events)
	}
	for _, scheme := range []string{"a", "stretch-text", "x+y", "x.y", "a1"} {
		if result := claim(t, bridge, "ext-a", scheme); result.Error != nil {
			t.Fatalf("claim of %q = %+v", scheme, result.Error)
		}
	}
}

func TestUIBridgeLinkClaimRefusesASchemeAnotherExtensionHolds(t *testing.T) {
	ui := newLinkUI()
	bridge := newTestBridge(ui)
	if result := claim(t, bridge, "ext-a", "stretchtext"); result.Error != nil {
		t.Fatal(result.Error)
	}
	result := claim(t, bridge, "ext-b", "stretchtext")
	if result.Error == nil || result.Error.Code != "scheme_in_use" {
		t.Fatalf("second claim = %+v, want scheme_in_use", result.Error)
	}
	if want := []string{"claim ext-a/stretchtext"}; !slices.Equal(ui.events, want) {
		t.Fatalf("events = %q, want %q", ui.events, want)
	}
}

func TestUIBridgeLinkClaimsEndWithTheirExtension(t *testing.T) {
	ui := newLinkUI()
	bridge := newTestBridge(ui)
	claim(t, bridge, "ext-a", "stretchtext")
	bridge.ClearExtension("ext-a")
	if want := []string{"claim ext-a/stretchtext", "release ext-a/stretchtext"}; !slices.Equal(ui.events, want) {
		t.Fatalf("events = %q, want %q", ui.events, want)
	}
	if result := claim(t, bridge, "ext-b", "stretchtext"); result.Error != nil {
		t.Fatalf("another extension could not take the scheme after the owner left: %+v", result.Error)
	}
}

func TestUIBridgeLinkClaimsMoveToANewUI(t *testing.T) {
	first := newLinkUI()
	bridge := newTestBridge(first)
	claim(t, bridge, "ext-a", "stretchtext")
	second := newLinkUI()
	bridge.SetUIContext(second)
	if want := []string{"claim ext-a/stretchtext", "release ext-a/stretchtext"}; !slices.Equal(first.events, want) {
		t.Fatalf("old UI events = %q, want %q", first.events, want)
	}
	if want := []string{"claim ext-a/stretchtext"}; !slices.Equal(second.events, want) {
		t.Fatalf("new UI events = %q, want %q", second.events, want)
	}
}

func TestUIBridgeLinkClickIsSentToTheExtensionConnection(t *testing.T) {
	ui := newLinkUI()
	bridge := newTestBridge(ui)
	hostEnd, extEnd := net.Pipe()
	t.Cleanup(func() { _ = extEnd.Close() })
	conn := NewConn("ext-a", hostEnd)
	conn.Start(t.Context())
	sent := make(chan *Envelope, 4)
	go func() {
		for {
			env, err := readEnvelopeFrom(extEnd)
			if err != nil {
				return
			}
			if env.Type == MsgNotify {
				sent <- env
			}
		}
	}()
	bridge.RegisterExtConn("ext-a", conn, false)
	claim(t, bridge, "ext-a", "stretchtext")
	ui.handlers["stretchtext"]("stretchtext:7")
	var got *Envelope
	select {
	case got = <-sent:
	case <-time.After(10 * time.Second):
		t.Fatal("the click never reached the extension's connection")
	}
	if got.Notify == nil || got.Notify.Method != NotifyLinkClick {
		t.Fatalf("sent %+v, want a %s notification", got, NotifyLinkClick)
	}
	var payload LinkClickPayload
	if err := json.Unmarshal(got.Notify.Args, &payload); err != nil || payload.Scheme != "stretchtext" || payload.URL != "stretchtext:7" {
		t.Fatalf("payload = %+v, %v", payload, err)
	}
	if !strings.HasPrefix(payload.URL, payload.Scheme+":") {
		t.Fatal("the address does not carry its scheme")
	}
}
