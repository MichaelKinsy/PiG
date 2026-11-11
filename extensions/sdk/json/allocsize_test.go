// SPDX-License-Identifier: MIT

package json

import (
	"math"
	"testing"
)

func TestSizeArithmeticSaturates(t *testing.T) {
	if got := addSize(3, 4); got != 7 {
		t.Errorf("addSize(3, 4) = %d", got)
	}
	if got := addSize(math.MaxInt, 1); got != math.MaxInt {
		t.Errorf("addSize(MaxInt, 1) = %d", got)
	}
	if got := addSize(-1, 2); got != math.MaxInt {
		t.Errorf("addSize(-1, 2) = %d", got)
	}
	if got := mulSize(2, 8); got != 16 {
		t.Errorf("mulSize(2, 8) = %d", got)
	}
	if got := mulSize(2, math.MaxInt/2+1); got != math.MaxInt {
		t.Errorf("mulSize(2, MaxInt/2+1) = %d", got)
	}
}
