// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"strings"
	"testing"
)

// BenchmarkInputDecodeRegexps is the decode of one input event by the patterns of the input path: an SGR mouse report (the alternate screen decodes
// one per mouse event, and a wheel or motion burst is hundreds a second), a key that is not a mouse report (isMouseSequence runs for every key in
// the alternate screen), a Kitty CSI-u key and a modifyOtherKeys key. It measures the regexps alone, without the rest of the input path.
func BenchmarkInputDecodeRegexps(b *testing.B) {
	b.Run("sgr-mouse-report", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := parseSgrMouseEvent("\x1b[<0;120;45M"); !ok {
				b.Fatal("not decoded")
			}
		}
	})
	b.Run("sgr-wheel-report", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := parseWheelEvent("\x1b[<65;120;45M"); !ok {
				b.Fatal("not decoded")
			}
		}
	})
	b.Run("is-mouse-sequence/key", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if isMouseSequence("a") {
				b.Fatal("a key is not a mouse report")
			}
		}
	})
	b.Run("is-mouse-sequence/mouse", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if !isMouseSequence("\x1b[<35;120;45M") {
				b.Fatal("a mouse report was not recognised")
			}
		}
	})
	b.Run("kitty-csi-u-key", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := DecodeKittyPrintable("\x1b[97u"); !ok {
				b.Fatal("not decoded")
			}
		}
	})
	b.Run("modify-other-keys", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := parseModifyOtherKeysSequence("\x1b[27;5;97~"); !ok {
				b.Fatal("not decoded")
			}
		}
	})
}

// BenchmarkFrameLineRegexps is the scan of one rendered line (120 columns) by the patterns that look at lines: the Kitty graphic escape that
// getRegisteredKittyImageMetadata looks for, on a line without one and on a line that holds one, and the OSC 133 prompt mark the prompt jump of the
// alternate screen looks for in the row it visits.
func BenchmarkFrameLineRegexps(b *testing.B) {
	plain := "\x1b[38;2;200;200;200m" + strings.Repeat("token ", 18) + "\x1b[0m"
	image := "\x1b_Ga=T,f=100,i=7,c=40,r=10;AAAA\x1b\\" + strings.Repeat(" ", 80)
	b.Run("kitty-metadata/plain-line", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := getRegisteredKittyImageMetadata(plain); ok {
				b.Fatal("a plain line holds no image")
			}
		}
	})
	b.Run("kitty-metadata/image-line", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, _ = getRegisteredKittyImageMetadata(image)
		}
	})
	b.Run("osc133-prompt-start/plain-line", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if osc133PromptStart.MatchString(plain) {
				b.Fatal("a plain line is no prompt")
			}
		}
	})
}
