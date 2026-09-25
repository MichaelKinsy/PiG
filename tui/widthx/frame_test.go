package widthx

import (
	"slices"
	"testing"
)

func TestFrameAt(t *testing.T) {
	wide := []string{"0123456789ab"} // 12 cells
	cases := []struct {
		name              string
		frameWidth, width int
		want              []string
	}{
		{"unknown width paints as rendered", 0, 10, wide},
		{"current width paints as rendered", 10, 10, wide},
		{"stale over-wide frame is not painted", 20, 10, nil},
		{"shrinking stale frame that fits is not painted", 20, 12, nil},
		{"widening stale frame is not painted", 10, 20, nil},
	}
	for _, tc := range cases {
		if got := FrameAt(wide, tc.frameWidth, tc.width); !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
	image := []string{"\x1b_Ga=T,f=100,i=7;" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" + "\x1b\\"}
	if got := FrameAt(image, 20, 10); got != nil {
		t.Errorf("stale image-only frame painted: %q", got)
	}
}
