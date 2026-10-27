package sdk

import "testing"

// The values are what Node 24.22.1 (V8) answers for Date.parse of each text.
var isoCanonical = map[string]float64{
	"1970-01-01T00:00:00.000Z": 0,
	"0000-01-01T00:00:00.000Z": -62167219200000,
	"0000-03-01T00:00:00.000Z": -62162035200000,
	"0001-01-01T00:00:00.000Z": -62135596800000,
	"9999-12-31T23:59:59.999Z": 253402300799999,
	"2000-02-29T12:34:56.789Z": 951827696789,
	"1900-02-28T23:59:59.999Z": -2203891200001,
	"1900-03-01T00:00:00.000Z": -2203891200000,
	"2100-03-01T00:00:00.000Z": 4107542400000,
	"2024-02-29T00:00:00.000Z": 1709164800000,
	"2026-10-24T18:30:15.042Z": 1792866615042,
	"1969-12-31T23:59:59.999Z": -1,
	"1600-02-29T00:00:00.000Z": -11670998400000,
	"2026-12-31T23:59:59.999Z": 1798761599999,
	"2026-01-01T00:00:00.001Z": 1767225600001,
}

// Texts V8 reads that the local parser must leave to the host: another form of the same instant, a field the legacy parser accepts although it is out of range
// (February 30 is March 2), and text that is not a date.
var isoLeftToTheHost = []string{
	"2026-01-02T03:04:05.678", "2026-01-02T03:04:05.678+00:00", "+012026-01-02T03:04:05.678Z", "-000001-01-01T00:00:00.000Z", "2026-01-02t03:04:05.678Z",
	"2026-01-02T03:04:05.678z", "2026-02-30T00:00:00.000Z", "2026-13-01T00:00:00.000Z", "2026-01-02T24:00:00.000Z", "2026-01-02T03:04:60.000Z",
	"2026-01-02T03:60:05.000Z", "2026-00-10T00:00:00.000Z", "2026-04-31T00:00:00.000Z", "2026-01-02 03:04:05.678Z", "2026-01-02T03:04:05.678Z\n",
	"２０２６-01-02T03:04:05.678Z", "2026-01-02T03:04:05Z", "2026-01-02T03:04:05.67Z", "2026-01-02T03:04:05.678Zx", "", "2026", "2026-01-02T0x:04:05.678Z",
	"2026-01-02T03:04:05.67:Z", "2026-01-02T03:04:0/.678Z", "202:-01-02T03:04:05.678Z", "2026-01-02T03:04:05.@78Z", "2026-01-00T00:00:00.000Z",
}

func TestParseISODateMatchesDateParse(t *testing.T) {
	for text, want := range isoCanonical {
		if got, ok := parseISODate(text); !ok || got != want {
			t.Errorf("parseISODate(%q) = %v, %v; Date.parse gives %v", text, got, ok, want)
		}
	}
}

func TestParseISODateLeavesOtherTextToTheHost(t *testing.T) {
	for _, text := range isoLeftToTheHost {
		if ms, ok := parseISODate(text); ok {
			t.Errorf("parseISODate(%q) = %v, want the host to answer", text, ms)
		}
	}
}

// A timestamp in the form Pi writes never reaches the host's parser, whose answer here is deliberately wrong so a call shows; any other text does.
func TestParseDateAsksTheHostOnlyForTextTheLocalParserLeaves(t *testing.T) {
	calls := 0
	SetHostHelpers(&HostHelpers{ParseDate: func(string) (float64, bool) {
		calls++
		return -7, true
	}})
	t.Cleanup(func() { SetHostHelpers(nil) })
	for text, want := range isoCanonical {
		if got, ok, err := ParseDate(text); err != nil || !ok || got != want {
			t.Errorf("ParseDate(%q) = %v, %v, %v; Date.parse gives %v", text, got, ok, err, want)
		}
	}
	if calls != 0 {
		t.Fatalf("the host parsed %d canonical timestamps", calls)
	}
	for i, text := range isoLeftToTheHost {
		if got, ok, err := ParseDate(text); err != nil || !ok || got != -7 {
			t.Errorf("ParseDate(%q) = %v, %v, %v; want the host's answer", text, got, ok, err)
		}
		if calls != i+1 {
			t.Fatalf("ParseDate(%q): %d host calls after %d texts", text, calls, i+1)
		}
	}
}

func BenchmarkParseISODate(b *testing.B) {
	b.ReportAllocs()
	ms := 0.0
	for b.Loop() {
		got, _ := parseISODate("2026-10-24T18:30:15.042Z")
		ms += got
	}
	if ms == 0 {
		b.Fatal("no time")
	}
}
