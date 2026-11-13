package viewcarrier

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestCarrierCarriesBindingsUnchanged: a wire client is written the bindings of a mount as the plugin sent them, in
// their order and with the spelling of each key, and its report of a binding it cannot honour reaches the plugin's
// diagnostics with the binding's place.
func TestCarrierCarriesBindingsUnchanged(t *testing.T) {
	f := newFixture(t)
	att := f.attach(4096)
	bindings := `[{"key":"shift+ctrl+w","intent":"add","arg":{},"name":"Week"},{"gesture":"swipe-left","intent":"wait"}]`
	ref := f.mount("cal", "month", intentDecls+`,"bindings":`+bindings)
	f.settled()
	events := f.w.events()
	if len(events) != 1 || !strings.Contains(events[0], `"bindings":`+bindings) {
		t.Fatalf("the client was written %q, want one snapshot with %s", events, bindings)
	}
	r := f.do(`{"id":"r","type":"ui_view_report","attachment":` + att + `,"plugin":"p","mount":{"key":"cal","epoch":` + strconv.FormatUint(ref.Epoch, 10) +
		`},"notice":{"code":"unsupported","severity":"info","path":"bindings[1]"}}`)
	if code(r) != "ok" {
		t.Fatalf("report = %v", r)
	}
	diag, _ := f.h.reg.Diagnostics("p", uiview.StateArgs{Key: "cal"})
	if !slices.ContainsFunc(diag.Notices, func(n uiview.Notice) bool {
		return n.Code == "unsupported" && n.Severity == uiview.SeverityInfo && n.Path == "bindings[1]"
	}) {
		t.Errorf("the plugin reads %+v", diag.Notices)
	}
}
