package ai

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// DateParseIn reads an ASCII string of up to dateParseStackUnits bytes from a stack buffer; it answers as it does for the UTF-16 conversion.
func TestDateParseInStackBufferMatchesUTF16(t *testing.T) {
	cases := []string{
		"", "2026-11-08T09:22:13.123Z", "Sun Nov 08 2026 09:22:13 GMT+0100", "2026-11-08", "garbage",
		strings.Repeat("1", dateParseStackUnits), strings.Repeat("1", dateParseStackUnits+1),
		"2026-11-08T09:22:13.123Z\u00a0", "2026-11-08T09:22:13.123Z\u00e9", "Nov 8 2026 \U0001F600",
	}
	for _, in := range cases {
		got, want := DateParseIn(in, time.UTC), jsDateParseUnits(jsstring.ToUTF16(in), time.UTC)
		if got != want && !(math.IsNaN(got) && math.IsNaN(want)) {
			t.Errorf("DateParseIn(%q) = %v, want %v", in, got, want)
		}
	}
}
