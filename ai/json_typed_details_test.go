package ai

import (
	"math"
	"strings"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// The shapes of internal/codingagent/tools' details (ReadDetails, EditToolDetails, BashDetails over tui.TruncationResult), which ai cannot import.
type typedTruncation struct {
	Content               string `json:"content"`
	Truncated             bool   `json:"truncated"`
	TruncatedBy           string `json:"truncatedBy"`
	TotalLines            int    `json:"totalLines"`
	TotalBytes            int    `json:"totalBytes"`
	OutputLines           int    `json:"outputLines"`
	OutputBytes           int    `json:"outputBytes"`
	LastLinePartial       bool   `json:"lastLinePartial"`
	FirstLineExceedsLimit bool   `json:"firstLineExceedsLimit"`
	MaxLines              int    `json:"maxLines"`
	MaxBytes              int    `json:"maxBytes"`
}

type typedReadDetails struct {
	Path       string           `json:"-"`
	StartLine  int              `json:"-"`
	TotalLines int              `json:"-"`
	Truncated  bool             `json:"-"`
	Truncation *typedTruncation `json:"truncation,omitempty"`
}

type typedEditDetails struct {
	Diff             string `json:"diff"`
	Patch            string `json:"patch"`
	FirstChangedLine int    `json:"firstChangedLine,omitempty"`
}

type typedBashDetails struct {
	Truncation     *typedTruncation `json:"truncation,omitempty"`
	FullOutputPath string           `json:"fullOutputPath,omitempty"`
}

type typedEmbedded struct {
	typedEditDetails
	Lines  []string `json:"lines"`
	Counts [2]uint8 `json:"counts"`
}

type typedFloat struct {
	Limit float64 `json:"limit"`
}
type typedFloatPointer struct {
	Limit *float64 `json:"limit"`
}
type typedInterface struct {
	Value any `json:"value"`
}
type typedMap struct {
	Values map[string]string `json:"values"`
}
type typedRecursive struct {
	Next *typedRecursive `json:"next"`
}
type typedSliceRecursive struct {
	Children []typedSliceRecursive `json:"children"`
}
type typedMarshaler struct{ Text string }

func (typedMarshaler) MarshalJSON() ([]byte, error) { return []byte(`{`), nil }

type typedPointerMarshaler struct{ Text string }

func (*typedPointerMarshaler) MarshalJSON() ([]byte, error) { return nil, nil }

type typedTextMarshaler string

func (typedTextMarshaler) MarshalText() ([]byte, error) { return nil, nil }

type typedZeroer struct{ Text string }

func (typedZeroer) IsZero() bool { panic("IsZero") }

type typedHoldsMarshaler struct {
	Inner typedMarshaler `json:"inner"`
}
type typedChan struct {
	C chan int `json:"c"`
}

// validateJsonValueByMarshal is validateJsonValue before the typed shortcut (PF1 N1): the reference it must agree with.
func validateJsonValueByMarshal(value any) error {
	if isPlainJSON(value, &plainWalk{}) {
		return nil
	}
	encoded, err := sdkjson.Marshal(value)
	if err != nil {
		return err
	}
	if sdkjson.Valid(encoded) {
		return nil
	}
	_, err = normalizeJSONValue(value)
	return err
}

func sameError(a, b error) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Error() == b.Error())
}

func TestTypedDetailsValidateAsTheyMarshal(t *testing.T) {
	nan, inf := math.NaN(), math.Inf(1)
	cycle := &typedRecursive{}
	cycle.Next = cycle
	for _, value := range []any{
		&typedReadDetails{}, (*typedReadDetails)(nil), &typedReadDetails{Path: "\xff", Truncation: &typedTruncation{Content: "a\xffb\xed\xa0\x80c\u2028<&>"}},
		&typedEditDetails{Diff: strings.Repeat("-x\n+y\n", 100)}, &typedBashDetails{FullOutputPath: "\x00\x1f"}, &typedEmbedded{Lines: []string{"\xc3"}},
		&typedFloat{Limit: nan}, &typedFloat{Limit: 2}, &typedFloatPointer{Limit: &inf}, &typedInterface{Value: nan}, &typedInterface{Value: "x"},
		&typedMap{Values: map[string]string{"\xff": "\xfe"}}, cycle, &typedRecursive{}, &typedMarshaler{}, &typedHoldsMarshaler{}, &typedChan{C: make(chan int)},
	} {
		if got, want := validateJsonValue(value), validateJsonValueByMarshal(value); !sameError(got, want) {
			t.Errorf("validateJsonValue(%T) = %v, the marshaling path %v", value, got, want)
		}
	}
}

func FuzzTypedDetailsValidateAsTheyMarshal(f *testing.F) {
	f.Add("content", "lines", 3, true)
	f.Add("a\xffb\xed\xa0\x80", "\u2028<>&\x00", -1, false)
	f.Fuzz(func(t *testing.T, content, by string, lines int, truncated bool) {
		for _, value := range []any{
			&typedReadDetails{Path: by, Truncation: &typedTruncation{Content: content, TruncatedBy: by, TotalLines: lines, Truncated: truncated}},
			&typedEditDetails{Diff: content, Patch: by, FirstChangedLine: lines},
			&typedEmbedded{typedEditDetails: typedEditDetails{Diff: content}, Lines: []string{by, content}},
		} {
			got, want := validateJsonValue(value), validateJsonValueByMarshal(value)
			if !sameError(got, want) || got != nil {
				t.Fatalf("validateJsonValue(%#v) = %v, the marshaling path %v", value, got, want)
			}
		}
	})
}

func readDetails50KB() *typedReadDetails {
	content := strings.Repeat("export const value = items.map((item) => item.id && item.ok < 3);\n", 800)
	return &typedReadDetails{Path: "src/a.ts", Truncation: &typedTruncation{Content: content, Truncated: true, TruncatedBy: "bytes", TotalLines: 2000, OutputLines: 800, OutputBytes: len(content), MaxLines: 2000, MaxBytes: 51200}}
}

// PF1 N1: a typed read result's details are valid JSON by construction; validating one must not encode its ~50 KB truncation text (pi-ai validates nothing).
func TestTypedDetailsValidateWithoutMarshaling(t *testing.T) {
	details := readDetails50KB()
	if allocs := testing.AllocsPerRun(20, func() {
		if err := validateJsonValue(details); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Fatalf("validating typed read details allocates %v times per call, want 0 (no encoding)", allocs)
	}
}

func BenchmarkValidateReadDetails50KB(b *testing.B) {
	details := readDetails50KB()
	b.ReportAllocs()
	for b.Loop() {
		if err := validateJsonValue(details); err != nil {
			b.Fatal(err)
		}
	}
}
