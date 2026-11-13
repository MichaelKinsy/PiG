// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package screenwriter

import (
	"strconv"
	"strings"
	"testing"
)

// modesAsText is what outside said of one private-mode sequence while it compared the modes as text: the reference the numbers it
// compares now are held to. params are the bytes between `CSI ?` and the final byte.
func modesAsText(params string, final byte) (screen bool, cursor int, forget bool, wrap int) {
	screen = true
	for mode := range strings.SplitSeq(params, ";") {
		switch mode {
		case "1049":
			screen, forget, wrap = final == 'h', true, -1
		case "47", "1047":
			forget, wrap = true, -1
		case "7":
			wrap = -1
			if final == 'l' {
				wrap = 1
			}
		case "25":
			cursor = -1
			if final == 'h' {
				cursor = 1
			}
		case "1", "12", "1000", "1002", "1003", "1004", "1005", "1006", "1015", "1016", "2004", "2026", "2027", "2031", "2048":
		default:
			forget = true
		}
	}
	return screen, cursor, forget, wrap
}

// A private mode is one outside knows only when it is written as that mode's number, in digits, without a leading zero: every number
// of up to five digits, alone, behind one and two zeros and beside the modes outside names, and the forms that are no such number
// (nothing, another parameter byte, an intermediate byte, a number that wraps around to a mode's when it is added up without a bound)
// say of the screen, the cursor, the rows and autowrap what the comparison of the modes as text said.
func TestOutsideReadsAPrivateModeOnlyAsItsNumber(t *testing.T) {
	named := []string{"1", "7", "12", "25", "47", "1000", "1002", "1003", "1004", "1005", "1006", "1015", "1016", "1047", "1049", "2004", "2026", "2027", "2031", "2048"}
	forms := []string{
		"", ";", "7;", ";7", "7;;25", "25;", ";1049", "7:1", "7:", ":7", "7<", "=7", "7?", "<", "7 ", "7$", "1049 ", "25!", ";;",
		"18446744073709551623", "18446744073709552665", "18446744073709551641", "4294967303", "4294968345",
	}
	for n := 0; n <= 99999; n++ {
		number := strconv.Itoa(n)
		forms = append(forms, number, "0"+number, "00"+number)
	}
	for _, a := range named {
		for _, b := range named {
			forms = append(forms, a+";"+b, a+";0"+b, "0"+a+";"+b, a+";"+b+";6")
		}
	}
	failures := 0
	for _, params := range forms {
		for _, final := range []byte{'h', 'l'} {
			data := "\x1b[?" + params + string(final)
			screen, cursor, forget, wrap := outside(data, true)
			wantScreen, wantCursor, wantForget, wantWrap := modesAsText(params, final)
			if screen != wantScreen || cursor != wantCursor || forget != wantForget || wrap != wantWrap {
				if failures++; failures <= 10 {
					t.Errorf("outside(%q) = screen %v, cursor %d, forget %v, autowrap %d; the modes as text say %v, %d, %v, %d",
						data, screen, cursor, forget, wrap, wantScreen, wantCursor, wantForget, wantWrap)
				}
			}
		}
	}
	if failures > 10 {
		t.Errorf("%d forms more differ", failures-10)
	}
}
