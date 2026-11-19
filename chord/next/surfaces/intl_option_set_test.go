package surfaces

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// D464 claim 3 listed "an empty option set that would print a date" among what `Intl` throws for. It does not throw: the pinned Node
// prints a date for `{}` (and for options that name no field, as a time zone alone). `TimeOptions{}` is outside the closed set of
// options PiG takes (a time needs a timeStyle or one of hour, minute, second; no Pi call site passes `{}`), so it is `ErrOption` here,
// and the record says so. The case is compared with the live Node, as the drift tests are.
func TestAnEmptyTimeOptionSetPrintsADateInNodeWherePiGReturnsErrOption(t *testing.T) {
	node := driftNode(t)
	at := time.Date(2026, time.June, 15, 14, 5, 7, 0, time.UTC)
	var cases []driftCase
	for _, locale := range []string{"en-US", "de", "ja"} {
		for _, options := range []map[string]any{nil, {"timeZone": "UTC"}} {
			cases = append(cases, driftCase{Kind: "time", Locale: &locale, MS: at.UnixMilli(), Options: options})
		}
	}
	for i, want := range runNode(t, node, cases) {
		if want.Err != "" || !strings.Contains(want.S, "2026") || strings.Contains(want.S, ":") {
			t.Fatalf("node for %s %v = %+v, want a date and no error", *cases[i].Locale, cases[i].Options, want)
		}
		lang := &Language{Locale: *cases[i].Locale}
		if got, err := FormatTime(lang, at, &TimeOptions{}); !errors.Is(err, ErrOption) || got != "" {
			t.Errorf("FormatTime(%s, TimeOptions{}) = %q, %v; node prints %q: want ErrOption", *cases[i].Locale, got, err, want.S)
		}
		if got, err := formatClock(lang, at, &TimeOptions{}); !errors.Is(err, ErrOption) || got != "" {
			t.Errorf("formatClock(%s, TimeOptions{}) = %q, %v: want ErrOption", *cases[i].Locale, got, err)
		}
	}
}
