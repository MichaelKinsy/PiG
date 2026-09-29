package tui

// Ports packages/tui/src/word-navigation.ts

import (
	"iter"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/wordsegmenter"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type SegmentData = wordsegmenter.SegmentData

// WordNavigationOptions replaces word segmentation or marks indivisible segments such as collapsed paste markers.
type WordNavigationOptions struct {
	Segment         func(text string) iter.Seq[SegmentData]
	IsAtomicSegment func(segment string) bool
}

func wordNavigationFunctions(options []WordNavigationOptions) (func(string) iter.Seq[SegmentData], func(string) bool) {
	segment := wordsegmenter.Segments
	atomic := func(string) bool { return false }
	if len(options) > 0 {
		if options[0].Segment != nil {
			segment = options[0].Segment
		}
		if options[0].IsAtomicSegment != nil {
			atomic = options[0].IsAtomicSegment
		}
	}
	return segment, atomic
}
func isWhitespaceChar(text string) bool { return strings.ContainsFunc(text, widthx.IsJSSpace) }

// FindWordBackward skips trailing whitespace, then one word-like or atomic segment or a non-word run. Offsets count UTF-16 units.
func FindWordBackward(text string, cursor int, options ...WordNavigationOptions) int {
	if cursor <= 0 {
		return 0
	}
	segment, atomic := wordNavigationFunctions(options)
	before := jsstring.Slice(text, 0, cursor)
	segments := slices.Collect(segment(before))
	next := cursor
	for len(segments) > 0 && !atomic(segments[len(segments)-1].Segment) && isWhitespaceChar(segments[len(segments)-1].Segment) {
		next -= jsstring.Length(segments[len(segments)-1].Segment)
		segments = segments[:len(segments)-1]
	}
	if len(segments) == 0 {
		return next
	}
	last := segments[len(segments)-1]
	switch {
	case atomic(last.Segment):
		next -= jsstring.Length(last.Segment)
	case last.IsWordLike:
		index := strings.LastIndexAny(last.Segment, punctuationChars)
		next -= jsstring.Length(last.Segment[index+1:])
	default:
		for len(segments) > 0 && !atomic(segments[len(segments)-1].Segment) && !segments[len(segments)-1].IsWordLike && !isWhitespaceChar(segments[len(segments)-1].Segment) {
			next -= jsstring.Length(segments[len(segments)-1].Segment)
			segments = segments[:len(segments)-1]
		}
	}
	return next
}

// FindWordForward skips leading whitespace, then one word-like or atomic segment or a non-word run. Offsets count UTF-16 units.
func FindWordForward(text string, cursor int, options ...WordNavigationOptions) int {
	length := jsstring.Length(text)
	if cursor >= length {
		return length
	}
	segment, atomic := wordNavigationFunctions(options)
	pull, stop := iter.Pull(segment(jsstring.Slice(text, cursor, length)))
	defer stop()
	current, ok := pull()
	next := cursor
	for ok && !atomic(current.Segment) && isWhitespaceChar(current.Segment) {
		next += jsstring.Length(current.Segment)
		current, ok = pull()
	}
	if !ok {
		return next
	}
	switch {
	case atomic(current.Segment):
		next += jsstring.Length(current.Segment)
	case current.IsWordLike:
		index := strings.IndexAny(current.Segment, punctuationChars)
		if index < 0 {
			index = len(current.Segment)
		}
		next += jsstring.Length(current.Segment[:index])
	default:
		for ok && !atomic(current.Segment) && !current.IsWordLike && !isWhitespaceChar(current.Segment) {
			next += jsstring.Length(current.Segment)
			current, ok = pull()
		}
	}
	return next
}
