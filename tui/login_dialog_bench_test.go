package tui

import "testing"

// A frame asks a settled dialog whether it changed (IsDirty, which takes the dialog's lock) and renders nothing.
func BenchmarkLoginDialogIsDirty(b *testing.B) {
	dialog := NewLoginDialogComponent(nil, "Bench", nil, "")
	dialog.ShowPrompt("Key:", "sk-")
	parent := NewContainer(dialog)
	parent.Render(contractWidth)
	parent.Render(contractWidth)
	b.ReportAllocs()
	for b.Loop() {
		dialog.IsDirty()
	}
}

// The frame of a parent whose child is a settled dialog: the parent's scan asks the dialog, and the lines are the ones it kept.
func BenchmarkLoginDialogSettledFrame(b *testing.B) {
	dialog := NewLoginDialogComponent(nil, "Bench", nil, "")
	dialog.ShowPrompt("Key:", "sk-")
	parent := NewContainer(dialog)
	parent.renderBorrowed(contractWidth)
	parent.renderBorrowed(contractWidth)
	b.ReportAllocs()
	for b.Loop() {
		parent.renderBorrowed(contractWidth)
	}
}
