package sdk

import (
	"math"
	"math/big"
	"strings"
)

// ResizedImage is Pi's ResizedImage (utils/image-resize-core.ts): the image as base64 data and its MIME type, and the dimensions FormatDimensionNote reads. Dimensions are
// JavaScript numbers. FormatDimensionNote reads only the dimensions, so a caller that has no image to hand it leaves Data and MimeType empty.
type ResizedImage struct {
	Data           string
	MimeType       string
	OriginalWidth  float64
	OriginalHeight float64
	Width          float64
	Height         float64
	WasResized     bool
}

// FormatDimensionNote is Pi's formatDimensionNote (utils/image-resize.ts:115-123, exported from index.ts): the note that tells the model how a resized image's
// coordinates map to the original, and false when the image was not resized.
func FormatDimensionNote(result ResizedImage) (string, bool) {
	if !result.WasResized {
		return "", false
	}
	scale := result.OriginalWidth / result.Width
	return "[Image: original " + jsNumberToString(result.OriginalWidth) + "x" + jsNumberToString(result.OriginalHeight) + ", displayed at " +
		jsNumberToString(result.Width) + "x" + jsNumberToString(result.Height) + ". Multiply coordinates by " + jsToFixed2(scale) +
		" to map to original image.]", true
}

// jsToFixed2 is Number.prototype.toFixed(2): the exact decimal value of the double rounded half up in magnitude (a tie goes to the larger
// n, which Go's and C's half-even formatting does not do for 0.125), "-" for any x < 0 even when it rounds to zero, and Number::toString from 1e21 on.
func jsToFixed2(value float64) string {
	if math.IsNaN(value) {
		return "NaN"
	}
	if math.Abs(value) >= 1e21 {
		return jsNumberToString(value)
	}
	sign := ""
	if value < 0 {
		sign = "-"
	}
	// -0 is not < 0, so it has no sign in JavaScript; big.Float would keep the sign of a negative zero.
	exact := new(big.Float).SetFloat64(math.Abs(value)).Text('f', 1100)
	whole, fraction, _ := strings.Cut(exact, ".")
	digits := []byte(whole + fraction[:2])
	if fraction[2] >= '5' {
		for i := len(digits) - 1; ; i-- {
			if i < 0 {
				digits = append([]byte{'1'}, digits...)
				break
			}
			if digits[i] != '9' {
				digits[i]++
				break
			}
			digits[i] = '0'
		}
	}
	text := string(digits)
	return sign + text[:len(text)-2] + "." + text[len(text)-2:]
}
