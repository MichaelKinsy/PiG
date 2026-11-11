package allocsize

import (
	"math"
	"testing"
)

func TestAdd(t *testing.T) {
	for _, tc := range []struct{ a, b, want int }{
		{0, 0, 0},
		{3, 4, 7},
		{math.MaxInt - 1, 1, math.MaxInt},
		{math.MaxInt, 0, math.MaxInt},
		{math.MaxInt, 1, math.MaxInt},
		{math.MaxInt/2 + 1, math.MaxInt/2 + 1, math.MaxInt},
		{-1, 5, math.MaxInt},
	} {
		if got := Add(tc.a, tc.b); got != tc.want {
			t.Errorf("Add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMul(t *testing.T) {
	for _, tc := range []struct{ a, b, want int }{
		{0, math.MaxInt, 0},
		{2, 8, 16},
		{2, math.MaxInt / 2, math.MaxInt - 1},
		{2, math.MaxInt/2 + 1, math.MaxInt},
		{math.MaxInt, 2, math.MaxInt},
		{1 << 32, 1 << 32, math.MaxInt},
		{-2, 3, math.MaxInt},
	} {
		if got := Mul(tc.a, tc.b); got != tc.want {
			t.Errorf("Mul(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// A saturated size makes the allocation panic instead of wrapping to a size that is too small.
func TestSaturatedSizePanicsInMake(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("make with a saturated size did not panic")
		}
	}()
	_ = make([]byte, 0, Add(math.MaxInt, 1))
}
