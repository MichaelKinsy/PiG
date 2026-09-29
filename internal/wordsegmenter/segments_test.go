package wordsegmenter

import (
	"slices"
	"testing"
)

func TestICUWordLikeRuleStatus(t *testing.T) {
	// Pinned ICU 78.3/Unicode 17 Intl.Segmenter observations. General Letter/Number categories alone lose Other_Number exclusions and ALetter symbols.
	for _, tc := range []struct {
		text string
		want []bool
	}{
		{"².", []bool{false, false}},
		{"½.", []bool{false, false}},
		{"Ⅷ.", []bool{true, false}},
		{"Ⓐ!", []bool{true, false}},
		{"׳", []bool{true}},
		{"1️⃣!", []bool{true, false}},
		{"\u0345", []bool{false}},
	} {
		t.Run(tc.text, func(t *testing.T) {
			var got []bool
			for segment := range Segments(tc.text) {
				got = append(got, segment.IsWordLike)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("word statuses=%v want=%v", got, tc.want)
			}
		})
	}
}
