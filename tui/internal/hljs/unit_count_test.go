package hljs

import "testing"

func TestUnitCountIsTheLengthOfTheUnits(t *testing.T) {
	for _, text := range []string{"", "abc", "héllo wörld", "日本語", "😀 emoji 𝔘", "a\xed\xa0\x80b", "\xed\xb0\x80", "bad \xff\xfe bytes", "tail \xe2\x82", "x\xf0\x9f"} {
		if got, want := unitCount(text), len(stringToUnits(text)); got != want {
			t.Errorf("unitCount(%q) = %d, want %d", text, got, want)
		}
	}
}
