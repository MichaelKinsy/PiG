package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func TestTextInputBasicFlow(t *testing.T) {
	ti := NewTextInput("Enter instructions")

	// Type some text
	ti.HandleInput("h")
	ti.HandleInput("e")
	ti.HandleInput("l")
	ti.HandleInput("l")
	ti.HandleInput("o")

	if ti.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", ti.Text(), "hello")
	}
	if ti.Done() {
		t.Error("should not be done yet")
	}

	// Confirm with Enter
	ti.HandleInput("\r")
	if !ti.Done() {
		t.Error("should be done after Enter")
	}
	if ti.Cancelled() {
		t.Error("should not be cancelled")
	}
	if ti.Text() != "hello" {
		t.Errorf("Text() = %q, want %q", ti.Text(), "hello")
	}
}

func TestTextInputRejectsC0DELAndC1ControlCharacters(t *testing.T) {
	for _, input := range []string{"\x00", "\x1f", "\x7f", "\u0085"} {
		t.Run(fmt.Sprintf("%q", input), func(t *testing.T) {
			text := NewTextInput("")
			text.HandleInput(input)
			if text.Text() != "" {
				t.Fatalf("control input inserted %q", text.Text())
			}
		})
	}
}

func TestTextInputSubmitsValueIncludingBackslashOnEnter(t *testing.T) {
	ti := NewTextInput("")
	for _, key := range []string{"h", "e", "l", "l", "o", "\\"} {
		ti.HandleInput(key)
	}
	ti.HandleInput("\r")
	if !ti.Done() {
		t.Fatal("expected input to be done after Enter")
	}
	if got := ti.Text(); got != "hello\\" {
		t.Fatalf("submitted text = %q want %q", got, "hello\\")
	}
}

func TestTextInputInsertsBackslashAsRegularCharacter(t *testing.T) {
	ti := NewTextInput("")
	ti.HandleInput("\\")
	ti.HandleInput("x")
	if got := ti.Text(); got != "\\x" {
		t.Fatalf("text = %q want %q", got, "\\x")
	}
}

func TestTextInputCancel(t *testing.T) {
	ti := NewTextInput("Prompt")
	ti.HandleInput("a")
	ti.HandleInput("b")
	ti.HandleInput("\x1b") // Esc

	if !ti.Done() {
		t.Error("should be done after Esc")
	}
	if !ti.Cancelled() {
		t.Error("should be cancelled after Esc")
	}
}

func TestTextInputBackspace(t *testing.T) {
	ti := NewTextInput("Test")
	ti.HandleInput("a")
	ti.HandleInput("b")
	ti.HandleInput("c")
	ti.HandleInput("\x7f") // backspace

	if ti.Text() != "ab" {
		t.Errorf("Text() = %q, want %q after backspace", ti.Text(), "ab")
	}
}

func TestTextInputCursorMovement(t *testing.T) {
	ti := NewTextInput("Test")
	ti.HandleInput("a")
	ti.HandleInput("b")
	ti.HandleInput("c")
	// Move left
	ti.HandleInput("\x1b[D") // left arrow
	ti.HandleInput("\x1b[D") // left arrow
	// Insert at position 1
	ti.HandleInput("X")

	if ti.Text() != "aXbc" {
		t.Errorf("Text() = %q, want %q after insert at cursor", ti.Text(), "aXbc")
	}
}

func TestTextInputCtrlAE(t *testing.T) {
	ti := NewTextInput("Test")
	ti.HandleInput("h")
	ti.HandleInput("i")

	// Ctrl+A (home)
	ti.HandleInput("\x01")
	ti.HandleInput("X")
	if ti.Text() != "Xhi" {
		t.Errorf("after Ctrl+A + X: got %q want %q", ti.Text(), "Xhi")
	}

	// Ctrl+E (end)
	ti.HandleInput("\x05")
	ti.HandleInput("Y")
	if ti.Text() != "XhiY" {
		t.Errorf("after Ctrl+E + Y: got %q want %q", ti.Text(), "XhiY")
	}
}

func TestTextInputRender(t *testing.T) {
	ti := NewTextInput("My Title")
	ti.HandleInput("t")
	ti.HandleInput("e")
	ti.HandleInput("s")
	ti.HandleInput("t")

	lines := ti.Render(80)
	if len(lines) != 1 {
		t.Fatalf("Render returned %d lines, want 1", len(lines))
	}
	if strings.Contains(lines[0], "My Title") {
		t.Fatalf("bare input line should not include title: %q", lines[0])
	}
	if !strings.Contains(lines[0], "> ") || !strings.Contains(lines[0], "test") {
		t.Fatalf("bare input line missing prompt/text: %q", lines[0])
	}
}

func TestTextInputRender_UsesCursorMarkerOnlyWhenFocused(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("abc")
	ti.Focused = true
	focused := ti.Render(20)[0]
	if !strings.Contains(focused, widthx.CursorMarker) {
		t.Fatalf("focused render missing cursor marker: %q", focused)
	}
	ti.Focused = false
	blurred := ti.Render(20)[0]
	if strings.Contains(blurred, widthx.CursorMarker) {
		t.Fatalf("blurred render should not contain cursor marker: %q", blurred)
	}
}

func TestTextInputSetText(t *testing.T) {
	ti := NewTextInput("Prefill")
	ti.SetText("existing")

	if ti.Text() != "existing" {
		t.Errorf("SetText: got %q want %q", ti.Text(), "existing")
	}

	// Cursor should be at end: typing appends
	ti.HandleInput("!")
	if ti.Text() != "existing!" {
		t.Errorf("after append: got %q want %q", ti.Text(), "existing!")
	}
}

func TestTextInputDeleteWordUndoAndYank(t *testing.T) {
	ti := NewTextInput("Prompt")
	ti.SetText("hello world")
	ti.HandleInput("\x17") // ctrl+w
	if got := ti.Text(); got != "hello " {
		t.Fatalf("after ctrl+w: got %q want %q", got, "hello ")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("after undo: got %q want %q", got, "hello world")
	}
	ti.HandleInput("\x17") // ctrl+w again
	ti.HandleInput("\x19") // ctrl+y yank
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("after yank: got %q want %q", got, "hello world")
	}
}

func TestTextInputCtrlWSavesDeletedTextAndYankRestoresAtCursor(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("foo bar baz")
	ti.HandleInput("\x05")
	ti.HandleInput("\x17")
	if got := ti.Text(); got != "foo bar " {
		t.Fatalf("after ctrl+w: got %q want %q", got, "foo bar ")
	}
	ti.HandleInput("\x01")
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "bazfoo bar " {
		t.Fatalf("after yank at start: got %q want %q", got, "bazfoo bar ")
	}
}

func TestTextInputCtrlUSavesDeletedTextToKillRing(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("hello world")
	ti.HandleInput("\x01")
	for range 6 {
		ti.HandleInput("\x1b[C")
	}
	ti.HandleInput("\x15")
	if got := ti.Text(); got != "world" {
		t.Fatalf("after ctrl+u: got %q want %q", got, "world")
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("after yank: got %q want %q", got, "hello world")
	}
}

func TestTextInputCtrlKSavesDeletedTextToKillRing(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("hello world")
	ti.HandleInput("\x01")
	ti.HandleInput("\x0b")
	if got := ti.Text(); got != "" {
		t.Fatalf("after ctrl+k: got %q want empty", got)
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("after yank: got %q want %q", got, "hello world")
	}
}

func TestTextInputYankDoesNothingWhenKillRingEmpty(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("test")
	ti.HandleInput("\x05")
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "test" {
		t.Fatalf("after yank with empty ring: got %q want %q", got, "test")
	}
}

func TestTextInputAltYCyclesKillRingAfterYank(t *testing.T) {
	ti := NewTextInput("")
	for _, text := range []string{"first", "second", "third"} {
		ti.SetText(text)
		ti.HandleInput("\x05")
		ti.HandleInput("\x17")
	}
	if got := ti.Text(); got != "" {
		t.Fatalf("after building kill ring: got %q want empty", got)
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "third" {
		t.Fatalf("after first yank: got %q want %q", got, "third")
	}
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "second" {
		t.Fatalf("after first alt+y: got %q want %q", got, "second")
	}
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "first" {
		t.Fatalf("after second alt+y: got %q want %q", got, "first")
	}
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "third" {
		t.Fatalf("after third alt+y: got %q want %q", got, "third")
	}
}

func TestTextInputAltYDoesNothingIfNotPrecededByYank(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("test")
	ti.HandleInput("\x05")
	ti.HandleInput("\x17")
	ti.SetText("other")
	ti.HandleInput("\x05")
	ti.HandleInput("x")
	if got := ti.Text(); got != "otherx" {
		t.Fatalf("after typing: got %q want %q", got, "otherx")
	}
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "otherx" {
		t.Fatalf("alt+y without yank should do nothing, got %q", got)
	}
}

func TestTextInputAltYDoesNothingWithSingleKillRingEntry(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("only")
	ti.HandleInput("\x05")
	ti.HandleInput("\x17")
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "only" {
		t.Fatalf("after yank: got %q want %q", got, "only")
	}
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "only" {
		t.Fatalf("alt+y with one kill-ring entry should do nothing, got %q", got)
	}
}

func TestTextInputConsecutiveCtrlWAccumulatesIntoOneKillEntry(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("one two three")
	ti.HandleInput("\x05")
	ti.HandleInput("\x17")
	ti.HandleInput("\x17")
	ti.HandleInput("\x17")
	if got := ti.Text(); got != "" {
		t.Fatalf("after consecutive ctrl+w: got %q want empty", got)
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "one two three" {
		t.Fatalf("after yank: got %q want %q", got, "one two three")
	}
}

func TestTextInputNonDeleteActionsBreakKillAccumulation(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("foo bar baz")
	ti.HandleInput("\x05")
	ti.HandleInput("\x17")
	if got := ti.Text(); got != "foo bar " {
		t.Fatalf("after first ctrl+w: got %q want %q", got, "foo bar ")
	}
	ti.HandleInput("x")
	if got := ti.Text(); got != "foo bar x" {
		t.Fatalf("after typing: got %q want %q", got, "foo bar x")
	}
	ti.HandleInput("\x17")
	if got := ti.Text(); got != "foo bar " {
		t.Fatalf("after second ctrl+w: got %q want %q", got, "foo bar ")
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "foo bar x" {
		t.Fatalf("after yank most recent kill: got %q want %q", got, "foo bar x")
	}
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "foo bar baz" {
		t.Fatalf("after alt+y rotation: got %q want %q", got, "foo bar baz")
	}
}

func TestTextInputNonYankActionsBreakAltYChain(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("first")
	ti.HandleInput("\x05")
	ti.HandleInput("\x17")
	ti.SetText("second")
	ti.HandleInput("\x05")
	ti.HandleInput("\x17")
	ti.SetText("")
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "second" {
		t.Fatalf("after yank: got %q want %q", got, "second")
	}
	ti.HandleInput("x")
	if got := ti.Text(); got != "secondx" {
		t.Fatalf("after typing: got %q want %q", got, "secondx")
	}
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "secondx" {
		t.Fatalf("alt+y after non-yank action should do nothing, got %q", got)
	}
}

func TestTextInputKillRingRotationPersistsAfterCycling(t *testing.T) {
	ti := NewTextInput("")
	for _, text := range []string{"first", "second", "third"} {
		ti.SetText(text)
		ti.HandleInput("\x05")
		ti.HandleInput("\x17")
	}
	ti.SetText("")
	ti.HandleInput("\x19")
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "second" {
		t.Fatalf("after alt+y cycle: got %q want %q", got, "second")
	}
	ti.HandleInput("x")
	ti.SetText("")
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "second" {
		t.Fatalf("fresh yank should preserve rotated ring order, got %q want %q", got, "second")
	}
}

func TestTextInputAltDDeletesWordForwardAndSavesToKillRing(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("hello world")
	ti.HandleInput("\x01")
	ti.HandleInput("\x1bd")
	if got := ti.Text(); got != " world" {
		t.Fatalf("after alt+d: got %q want %q", got, " world")
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("after yank: got %q want %q", got, "hello world")
	}
}

func TestTextInputBackwardDeletesPrependForwardDeletesAppendDuringAccumulation(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("prefix|suffix")
	ti.HandleInput("\x01")
	for range 6 {
		ti.HandleInput("\x1b[C")
	}
	ti.HandleInput("\x0b")
	if got := ti.Text(); got != "prefix" {
		t.Fatalf("after ctrl+k: got %q want %q", got, "prefix")
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "prefix|suffix" {
		t.Fatalf("after yank: got %q want %q", got, "prefix|suffix")
	}
}

func TestTextInputAltDAccumulatesForwardDeletesIntoOneKillEntry(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("hello world test")
	ti.HandleInput("\x01")
	ti.HandleInput("\x1bd")
	if got := ti.Text(); got != " world test" {
		t.Fatalf("after first alt+d: got %q want %q", got, " world test")
	}
	ti.HandleInput("\x1bd")
	if got := ti.Text(); got != " test" {
		t.Fatalf("after second alt+d: got %q want %q", got, " test")
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "hello world test" {
		t.Fatalf("after yank accumulated delete: got %q want %q", got, "hello world test")
	}
}

func TestTextInputYankInMiddleOfText(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("word")
	ti.HandleInput("\x05")
	ti.HandleInput("\x17")
	ti.SetText("hello world")
	ti.HandleInput("\x01")
	for range 6 {
		ti.HandleInput("\x1b[C")
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "hello wordworld" {
		t.Fatalf("yank in middle: got %q want %q", got, "hello wordworld")
	}
}

func TestTextInputYankPopInMiddleOfText(t *testing.T) {
	ti := NewTextInput("")
	for _, text := range []string{"FIRST", "SECOND"} {
		ti.SetText(text)
		ti.HandleInput("\x05")
		ti.HandleInput("\x17")
	}
	ti.SetText("hello world")
	ti.HandleInput("\x01")
	for range 6 {
		ti.HandleInput("\x1b[C")
	}
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "hello SECONDworld" {
		t.Fatalf("after yank: got %q want %q", got, "hello SECONDworld")
	}
	ti.HandleInput("\x1by")
	if got := ti.Text(); got != "hello FIRSTworld" {
		t.Fatalf("after yank-pop: got %q want %q", got, "hello FIRSTworld")
	}
}

func TestTextInputUndoDoesNothingWhenStackEmpty(t *testing.T) {
	ti := NewTextInput("")
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "" {
		t.Fatalf("undo on empty stack changed text to %q", got)
	}
}

func TestTextInputUndoCoalescesWordCharsAndSpacesSeparately(t *testing.T) {
	ti := NewTextInput("")
	for _, key := range []string{"h", "e", "l", "l", "o", " ", "w", "o", "r", "l", "d"} {
		ti.HandleInput(key)
	}
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("after typing: got %q want %q", got, "hello world")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello" {
		t.Fatalf("after first undo: got %q want %q", got, "hello")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "" {
		t.Fatalf("after second undo: got %q want empty", got)
	}
}

func TestTextInputUndoesSpacesOneAtATime(t *testing.T) {
	ti := NewTextInput("")
	for _, key := range []string{"h", "e", "l", "l", "o", " ", " "} {
		ti.HandleInput(key)
	}
	if got := ti.Text(); got != "hello  " {
		t.Fatalf("after typing: got %q want %q", got, "hello  ")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello " {
		t.Fatalf("after first undo: got %q want %q", got, "hello ")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello" {
		t.Fatalf("after second undo: got %q want %q", got, "hello")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "" {
		t.Fatalf("after third undo: got %q want empty", got)
	}
}

func TestTextInputUndoesBackspaceAndForwardDelete(t *testing.T) {
	ti := NewTextInput("")
	for _, key := range []string{"h", "e", "l", "l", "o"} {
		ti.HandleInput(key)
	}
	ti.HandleInput("\x7f")
	if got := ti.Text(); got != "hell" {
		t.Fatalf("after backspace: got %q want %q", got, "hell")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello" {
		t.Fatalf("undo backspace: got %q want %q", got, "hello")
	}
	ti.HandleInput("\x01")
	ti.HandleInput("\x1b[C")
	ti.HandleInput("\x1b[3~")
	if got := ti.Text(); got != "hllo" {
		t.Fatalf("after forward delete: got %q want %q", got, "hllo")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello" {
		t.Fatalf("undo forward delete: got %q want %q", got, "hello")
	}
}

func TestTextInputUndoesKillAndYankOperations(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("hello world")
	ti.HandleInput("\x17")
	if got := ti.Text(); got != "hello " {
		t.Fatalf("after ctrl+w: got %q want %q", got, "hello ")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("undo ctrl+w: got %q want %q", got, "hello world")
	}

	ti.SetText("hello world")
	ti.HandleInput("\x01")
	for range 6 {
		ti.HandleInput("\x1b[C")
	}
	ti.HandleInput("\x0b")
	if got := ti.Text(); got != "hello " {
		t.Fatalf("after ctrl+k: got %q want %q", got, "hello ")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("undo ctrl+k: got %q want %q", got, "hello world")
	}

	ti.SetText("hello world")
	ti.HandleInput("\x01")
	for range 6 {
		ti.HandleInput("\x1b[C")
	}
	ti.HandleInput("\x15")
	if got := ti.Text(); got != "world" {
		t.Fatalf("after ctrl+u: got %q want %q", got, "world")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("undo ctrl+u: got %q want %q", got, "hello world")
	}

	ti.SetText("hello ")
	ti.HandleInput("\x17")
	ti.HandleInput("\x19")
	if got := ti.Text(); got != "hello " {
		t.Fatalf("after yank: got %q want %q", got, "hello ")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "" {
		t.Fatalf("undo yank: got %q want empty", got)
	}
}

func TestTextInputUndoesAltD(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("hello world")
	ti.HandleInput("\x01")
	ti.HandleInput("\x1bd")
	if got := ti.Text(); got != " world" {
		t.Fatalf("after alt+d: got %q want %q", got, " world")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("undo alt+d: got %q want %q", got, "hello world")
	}
}

func TestTextInputUndoesPasteAtomically(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("hello world")
	ti.HandleInput("\x01") // Ctrl+A
	for range 5 {
		ti.HandleInput("\x1b[C")
	}
	ti.HandleInput("\x1b[200~beep boop\x1b[201~")
	if got := ti.Text(); got != "hellobeep boop world" {
		t.Fatalf("after paste: got %q want %q", got, "hellobeep boop world")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "hello world" {
		t.Fatalf("after undo: got %q want %q", got, "hello world")
	}
}

func TestTextInputCursorMovementStartsNewUndoUnit(t *testing.T) {
	ti := NewTextInput("")
	ti.HandleInput("a")
	ti.HandleInput("b")
	ti.HandleInput("c")
	ti.HandleInput("\x01") // Ctrl+A
	ti.HandleInput("\x05") // Ctrl+E
	ti.HandleInput("d")
	ti.HandleInput("e")
	if got := ti.Text(); got != "abcde" {
		t.Fatalf("after typing: got %q want %q", got, "abcde")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "abc" {
		t.Fatalf("after first undo: got %q want %q", got, "abc")
	}
	ti.HandleInput(hostUndoInput())
	if got := ti.Text(); got != "" {
		t.Fatalf("after second undo: got %q want empty", got)
	}
}

func TestTextInputBracketedPasteCleansInput(t *testing.T) {
	ti := NewTextInput("Prompt")
	ti.HandleInput("\x1b[200~hello\nwor\trld\x1b[201~")
	if got := ti.Text(); got != "hellowor    rld" {
		t.Fatalf("paste result = %q want %q", got, "hellowor    rld")
	}
}

func TestTextInputRenderHorizontalScrollShowsTail(t *testing.T) {
	ti := NewTextInput("Prompt")
	ti.SetText("abcdefghijklmnopqrstuvwxyz")
	lines := ti.Render(18)
	if len(lines) != 1 {
		t.Fatalf("Render returned %d lines, want 1", len(lines))
	}
	if !strings.Contains(lines[0], "uvwxyz") {
		t.Fatalf("input line %q missing tail of long input", lines[0])
	}
}

func TestTextInputRenderDoesNotOverflowWideText(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("これはテスト文章です。日本語のテキストが正しく表示されるかどうかを確認するためのサンプルテキストです。あいうえお")
	ti.Focused = true
	ti.HandleInput("\x01")
	for range 10 {
		ti.HandleInput("\x1b[C")
	}
	lines := ti.Render(20)
	if len(lines) != 1 {
		t.Fatalf("Render returned %d lines, want 1", len(lines))
	}
	if got := widthx.VisibleWidth(lines[0]); got > 20 {
		t.Fatalf("rendered line overflowed: visible=%d line=%q", got, lines[0])
	}
}

func TestTextInputRenderDoesNotOverflowWideTextMatrix(t *testing.T) {
	tests := []string{
		"가나다라마바사아자차카타파하 한글 텍스트가 터미널 너비를 초과하면 크래시가 발생합니다 이것은 재현용 테스트입니다",
		"これはテスト文章です。日本語のテキストが正しく表示されるかどうかを確認するためのサンプルテキストです。あいうえお",
		"这是一段测试文本，用于验证中文字符在终端中的显示宽度是否被正确计算，如果不正确就会导致用户界面崩溃的问题",
		"ＡＢＣＤＥＦＧＨＩＪＫＬＭＮＯＰＱＲＳＴＵＶＷＸＹＺ０１２３４５６７８９ａｂｃｄｅｆｇｈｉｊｋｌｍ",
	}
	moves := []struct {
		name string
		move func(*TextInput)
	}{
		{name: "start", move: func(_ *TextInput) {}},
		{name: "middle", move: func(ti *TextInput) {
			for range 10 {
				ti.HandleInput("\x1b[C")
			}
		}},
		{name: "end", move: func(ti *TextInput) { ti.HandleInput("\x05") }},
	}
	for _, text := range tests {
		for _, move := range moves {
			t.Run(move.name, func(t *testing.T) {
				ti := NewTextInput("")
				ti.SetText(text)
				ti.Focused = true
				move.move(ti)
				lines := ti.Render(93)
				if len(lines) != 1 {
					t.Fatalf("Render returned %d lines, want 1", len(lines))
				}
				if got := widthx.VisibleWidth(lines[0]); got > 93 {
					t.Fatalf("rendered line overflowed: visible=%d line=%q", got, lines[0])
				}
			})
		}
	}
}

func TestTextInputKeepsCursorVisibleWhenHorizontallyScrollingWideText(t *testing.T) {
	ti := NewTextInput("")
	ti.SetText("가나다라마바사아자차카타파하")
	ti.Focused = true
	ti.HandleInput("\x01")
	for range 5 {
		ti.HandleInput("\x1b[C")
	}
	lines := ti.Render(20)
	if len(lines) != 1 {
		t.Fatalf("Render returned %d lines, want 1", len(lines))
	}
	if got := widthx.VisibleWidth(lines[0]); got > 20 {
		t.Fatalf("rendered line overflowed: visible=%d line=%q", got, lines[0])
	}
}
