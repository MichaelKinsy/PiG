package jsonwire

import "testing"

func TestHasLoneSurrogateEscape(t *testing.T) {
	for text, want := range map[string]bool{
		``: false, `"abc"`: false, `"\u0041"`: false, `"\ud83d\ude00"`: false, `"\uD83D\uDE00x\uD83D\uDE00"`: false,
		`"\ud800"`: true, `"\udc00"`: true, `"\ud83d"`: true, `"\ud83dx"`: true, `"\ud83d\u0041"`: true, `"\ude00\ud83d"`: true,
		`"\ud83d\ud83d\ude00"`: true, `"\\ud800"`: true, `"\ud83d\ude00\ud800"`: true, `"\u00e9\uD7FF\ue000"`: false,
	} {
		if got := HasLoneSurrogateEscape([]byte(text)); got != want {
			t.Errorf("%q: %v, want %v", text, got, want)
		}
	}
}
