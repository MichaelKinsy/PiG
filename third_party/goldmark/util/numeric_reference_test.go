// SPDX-License-Identifier: MIT

package util

import "testing"

func TestResolveNumericReferencesBeyondUnicodeIsReplacementCharacter(t *testing.T) {
	for input, want := range map[string]string{
		"&#x41;":             "A",
		"&#65;":              "A",
		"&#x10FFFF;":         "\U0010FFFF",
		"&#x110000;":         "\uFFFD",
		"&#xFFFFFFFF;":       "\uFFFD",
		"&#x1FFFFFFFFFFFFF;": "\uFFFD",
		"&#1114112;":         "\uFFFD",
		"&#9999999;":         "\uFFFD",
		"&#x80000000;":       "\uFFFD",
		"&#0;":               "\uFFFD",
		"&#xD800;":           "\uFFFD",
	} {
		if got := string(ResolveNumericReferences([]byte(input))); got != want {
			t.Errorf("ResolveNumericReferences(%q) = %q, want %q", input, got, want)
		}
	}
}
