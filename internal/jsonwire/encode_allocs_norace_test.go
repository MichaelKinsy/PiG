// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build !race

package jsonwire

import (
	"encoding/json/jsontext"
	"io"
	"strings"
	"testing"
)

// A text over the bound of the pooled buffer costs the allocation of its own growth and no more. WriteStringify used to drop the holder of
// the buffer it could not keep, so the pool made a new holder for the next call: one allocation more for every text over maxPooledText.
func TestWriteStringifyOfATextOverThePoolBoundAllocatesOnlyTheTextsGrowth(t *testing.T) {
	for _, size := range []int{64, 1 << 10, maxPooledText - 2, maxPooledText, 4 * maxPooledText} {
		text := `"` + strings.Repeat("x", size) + `"`
		appendText := func(dst []byte) ([]byte, error) { return append(dst, text...), nil }
		encoder := jsontext.NewEncoder(io.Discard)
		if err := WriteStringify(encoder, appendText); err != nil {
			t.Fatal(err)
		}
		own := testing.AllocsPerRun(200, func() { _, _ = appendText(nil) })
		got := testing.AllocsPerRun(200, func() {
			if err := WriteStringify(encoder, appendText); err != nil {
				t.Fatal(err)
			}
		})
		// A text that fits the pooled buffer allocates nothing once the pool is warm (the first call above); one over the bound grows a buffer of its own,
		// as the appender does for itself.
		want := 0.0
		if len(text) > maxPooledText {
			want = own
		}
		if got > want {
			t.Errorf("a text of %d bytes allocated %v times, want %v", len(text), got, want)
		}
	}
}
