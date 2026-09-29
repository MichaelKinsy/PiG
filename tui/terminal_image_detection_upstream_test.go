package tui

import (
	"strings"
	"testing"
)

func TestUpstreamTerminalImageDetection(t *testing.T) {
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:74
	t.Run("should detect iTerm2 image escape sequence at start of line", func(t *testing.T) {
		iterm2ImageLine := "\u001b]1337;File=size=100,100;inline=1:base64encodeddata==\u0007"
		if got := IsImageLine(iterm2ImageLine); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:80
	t.Run("should detect iTerm2 image escape sequence with text before it", func(t *testing.T) {
		lineWithTextAndImage := "Some text \u001b]1337;File=size=100,100;inline=1:base64data==\u0007 more text"
		if got := IsImageLine(lineWithTextAndImage); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:86
	t.Run("should detect iTerm2 image escape sequence in middle of long line", func(t *testing.T) {
		longLineWithImage := "Text before image..." + "\u001b]1337;File=inline=1:verylongbase64data==" + "...text after"
		if got := IsImageLine(longLineWithImage); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:93
	t.Run("should detect iTerm2 image escape sequence at end of line", func(t *testing.T) {
		lineWithImageAtEnd := "Regular text ending with \u001b]1337;File=inline=1:base64data==\u0007"
		if got := IsImageLine(lineWithImageAtEnd); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:98
	t.Run("should detect minimal iTerm2 image escape sequence", func(t *testing.T) {
		minimalImageLine := "\u001b]1337;File=:\u0007"
		if got := IsImageLine(minimalImageLine); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:105
	t.Run("should detect Kitty image escape sequence at start of line", func(t *testing.T) {
		kittyImageLine := "\u001b_Ga=T,f=100,t=f,d=base64data...\u001b\\\u001b_Gm=i=1;\u001b\\"
		if got := IsImageLine(kittyImageLine); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:111
	t.Run("should detect Kitty image escape sequence with text before it", func(t *testing.T) {
		lineWithTextAndKittyImage := "Output: \u001b_Ga=T,f=100;data...\u001b\\\u001b_Gm=i=1;\u001b\\"
		if got := IsImageLine(lineWithTextAndKittyImage); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:117
	t.Run("should detect Kitty image escape sequence with padding", func(t *testing.T) {
		kittyWithPadding := "  \u001b_Ga=T,f=100...\u001b\\\u001b_Gm=i=1;\u001b\\  "
		if got := IsImageLine(kittyWithPadding); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:125
	t.Run("should detect image sequences in very long lines (304k+ chars)", func(t *testing.T) {
		base64Char := strings.Repeat("A", 100)
		imageSequence := "\u001b]1337;File=size=800,600;inline=1:"
		longLine := "Text prefix " + imageSequence + strings.Repeat(base64Char, 3000) + " suffix"
		if got := len(longLine) > 300000; got != true {
			t.Fatalf("got %v, want true", got)
		}
		if got := IsImageLine(longLine); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:142
	t.Run("should detect image sequences when terminal doesn't support images", func(t *testing.T) {
		lineWithImage := "Read image file [image/jpeg]\u001b]1337;File=inline=1:base64data==\u0007"
		if got := IsImageLine(lineWithImage); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:149
	t.Run("should detect image sequences with ANSI codes before them", func(t *testing.T) {
		lineWithAnsiAndImage := "\u001b[31mError output \u001b]1337;File=inline=1:image==\u0007"
		if got := IsImageLine(lineWithAnsiAndImage); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:155
	t.Run("should detect image sequences with ANSI codes after them", func(t *testing.T) {
		lineWithImageAndAnsi := "\u001b_Ga=T,f=100:data...\u001b\\\u001b_Gm=i=1;\u001b\\\u001b[0m reset"
		if got := IsImageLine(lineWithImageAndAnsi); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:162
	t.Run("should not detect images in plain text lines", func(t *testing.T) {
		plainText := "This is just a regular text line without any escape sequences"
		if got := IsImageLine(plainText); got != false {
			t.Fatalf("got %v, want false", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:167
	t.Run("should not detect images in lines with only ANSI codes", func(t *testing.T) {
		ansiText := "\u001b[31mRed text\u001b[0m and \u001b[32mgreen text\u001b[0m"
		if got := IsImageLine(ansiText); got != false {
			t.Fatalf("got %v, want false", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:172
	t.Run("should not detect images in lines with cursor movement codes", func(t *testing.T) {
		cursorCodes := "\u001b[1A\u001b[2KLine cleared and moved up"
		if got := IsImageLine(cursorCodes); got != false {
			t.Fatalf("got %v, want false", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:177
	t.Run("should not detect images in lines with partial iTerm2 sequences", func(t *testing.T) {
		partialSequence := "Some text with ]1337;File but missing ESC at start"
		if got := IsImageLine(partialSequence); got != false {
			t.Fatalf("got %v, want false", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:183
	t.Run("should not detect images in lines with partial Kitty sequences", func(t *testing.T) {
		partialSequence := "Some text with _G but missing ESC at start"
		if got := IsImageLine(partialSequence); got != false {
			t.Fatalf("got %v, want false", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:189
	t.Run("should not detect images in empty lines", func(t *testing.T) {
		if got := IsImageLine(""); got != false {
			t.Fatalf("got %v, want false", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:193
	t.Run("should not detect images in lines with newlines only", func(t *testing.T) {
		if got := IsImageLine("\n"); got != false {
			t.Fatalf("got %v, want false", got)
		}
		if got := IsImageLine("\n\n"); got != false {
			t.Fatalf("got %v, want false", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:200
	t.Run("should detect images when line has both Kitty and iTerm2 sequences", func(t *testing.T) {
		mixedLine := "Kitty: \u001b_Ga=T...\u001b\\\u001b_Gm=i=1;\u001b\\ iTerm2: \u001b]1337;File=inline=1:data==\u0007"
		if got := IsImageLine(mixedLine); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:205
	t.Run("should detect image in line with multiple text and image segments", func(t *testing.T) {
		complexLine := "Start \u001b]1337;File=img1==\u0007 middle \u001b]1337;File=img2==\u0007 end"
		if got := IsImageLine(complexLine); got != true {
			t.Fatalf("got %v, want true", got)
		}
	})
	// .upstream/v0.87.1/packages/tui/test/terminal-image.test.ts:210
	t.Run("should not falsely detect image in line with file path containing keywords", func(t *testing.T) {
		filePathLine := "/path/to/File_1337_backup/image.jpg"
		if got := IsImageLine(filePathLine); got != false {
			t.Fatalf("got %v, want false", got)
		}
	})
}
