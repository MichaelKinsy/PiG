package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// A resumed or extension-supplied shell result carries its details as decoded JSON. Reading the truncation fields from
// them must not encode the whole truncation object (whose content member holds the kept output) and decode it again: the
// cost is the same for 1 KiB and 256 KiB of truncated content.
func TestShellDetailsFromReadsTheMapWithoutARoundTrip(t *testing.T) {
	details := func(size int) any {
		var decoded any
		raw, err := json.Marshal(map[string]any{
			"fullOutputPath": "/tmp/pi-bash-1.log",
			"truncation":     map[string]any{"content": strings.Repeat("kept output line\n", size/17), "truncated": true, "truncatedBy": "lines", "totalLines": 9000, "outputLines": 2000, "maxBytes": 51200, "totalBytes": 1 << 20},
		})
		if err != nil || json.Unmarshal(raw, &decoded) != nil {
			t.Fatal(err)
		}
		return decoded
	}
	small, large := details(1<<10), details(256<<10)
	got := shellDetailsFrom(large)
	if got.FullOutputPath != "/tmp/pi-bash-1.log" || got.Truncation == nil || !got.Truncation.Truncated || got.Truncation.TruncatedBy != "lines" || got.Truncation.TotalLines != 9000 || got.Truncation.OutputLines != 2000 || got.Truncation.MaxBytes != 51200 {
		t.Fatalf("details read as %+v / %+v", got, got.Truncation)
	}
	smallCost := renderBytesPerCall(50, func() { shellDetailsFrom(small) })
	largeCost := renderBytesPerCall(50, func() { shellDetailsFrom(large) })
	t.Logf("%.0f B per read with 1 KiB of content, %.0f B with 256 KiB", smallCost, largeCost)
	if largeCost > 2*smallCost+1024 {
		t.Fatalf("reading the details round-trips the content: %.0f B with 1 KiB, %.0f B with 256 KiB", smallCost, largeCost)
	}
}

// The map is read by its exact keys, as bash.ts reads truncation.truncatedBy and the counts: a member of the wrong type or a
// fractional count reads as no truncation object, a Go number an in-process extension supplied reads as its value, and a
// key that matches a field only case-insensitively is another property.
func TestShellDetailsFromReadsTheExactKeys(t *testing.T) {
	cases := []struct {
		truncation map[string]any
		want       *tools.TruncationResult
	}{
		{map[string]any{"truncated": true, "truncatedBy": "bytes", "totalLines": 3.0, "outputLines": 2.0, "maxBytes": 10.0}, &tools.TruncationResult{Truncated: true, TruncatedBy: "bytes", TotalLines: 3, OutputLines: 2, MaxBytes: 10}},
		{map[string]any{"truncated": "yes", "totalLines": 3.0}, nil},
		{map[string]any{"truncated": true, "totalLines": 2.5}, nil},
		{map[string]any{"truncated": true, "totalLines": 1e300}, nil},
		{map[string]any{"truncated": true, "totalLines": []any{1.0}}, nil},
		{map[string]any{"truncated": true, "Truncated": false, "outputLines": 4.0}, &tools.TruncationResult{Truncated: true, OutputLines: 4}},
		{map[string]any{"truncated": false, "Truncated": true}, &tools.TruncationResult{}},
		{map[string]any{"TRUNCATEDBY": "lines", "truncated": true}, &tools.TruncationResult{Truncated: true}},
		{map[string]any{"truncated": true, "totallines": 9.0, "totalLines": 5.0}, &tools.TruncationResult{Truncated: true, TotalLines: 5}},
		{map[string]any{"truncated": nil, "maxBytes": nil}, &tools.TruncationResult{}},
		{map[string]any{"truncated": true, "totalLines": json.Number("7"), "outputLines": json.Number("7.5")}, nil},
		{map[string]any{"truncated": true, "totalLines": json.Number("7")}, &tools.TruncationResult{Truncated: true, TotalLines: 7}},
		{map[string]any{"truncated": true, "totalLines": 9000, "outputLines": int64(2000), "maxBytes": uint32(51200)}, &tools.TruncationResult{Truncated: true, TotalLines: 9000, OutputLines: 2000, MaxBytes: 51200}},
		{map[string]any{"truncated": true, "totalLines": uint64(1 << 63)}, nil},
		{map[string]any{}, &tools.TruncationResult{}},
	}
	for _, c := range cases {
		got := shellDetailsFrom(map[string]any{"truncation": c.truncation})
		if (got.Truncation == nil) != (c.want == nil) || got.Truncation != nil && *got.Truncation != *c.want {
			t.Errorf("%v: read %+v, want %+v", c.truncation, got.Truncation, c.want)
		}
	}
}

// bash.ts tests truncation.truncatedBy === "lines": a TRUNCATEDBY member is not truncatedBy, so the card shows the
// byte-limit warning, not the line counts.
func TestShellCardIgnoresACaseVariantTruncatedBy(t *testing.T) {
	details := map[string]any{"truncation": map[string]any{"truncated": true, "TRUNCATEDBY": "lines", "outputLines": 2.0, "totalLines": 30.0}}
	rendered := strings.Join(makeShellBodyRenderer("one\ntwo", details, false, nil)(80, true), "\n")
	if strings.Contains(rendered, "Truncated: showing") || !strings.Contains(rendered, "Truncated: 2 lines shown (50.0KB limit)") {
		t.Fatalf("card rendered\n%s\nwant the byte-limit warning", rendered)
	}
}
