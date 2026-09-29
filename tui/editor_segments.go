package tui

// Ports packages/tui/src/components/editor.ts

import (
	"iter"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/wordsegmenter"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// editorSegment uses UTF-16 source offsets and terminal-cell width.
type editorSegment struct {
	Text       string
	Start, End int
	Width      int
}

// segmentLine retains the source's UTF-16 offsets, including gaps left by a base grapheme that straddles a marker's end.
func (e *Editor) segmentLine(text string) []editorSegment {
	return editorSegments(e.segment(text, "grapheme"))
}

func editorSegments(segments iter.Seq[SegmentData]) []editorSegment {
	var result []editorSegment
	for segment := range segments {
		result = append(result, editorSegment{Text: segment.Segment, Start: segment.Index, End: segment.Index + jsstring.Length(segment.Segment), Width: widthx.VisibleWidth(segment.Segment)})
	}
	return result
}

func graphemeSegmentData(text string) iter.Seq[SegmentData] {
	return func(yield func(SegmentData) bool) {
		index := 0
		for rest := text; rest != ""; {
			var part string
			part, rest = widthx.FirstGrapheme(rest)
			if !yield(SegmentData{Segment: part, Index: index, Input: text}) {
				return
			}
			index += jsstring.Length(part)
		}
	}
}

// segmentWithMarkers merges only registered paste IDs, using the same spans for word and grapheme segmentation. Indices count UTF-16 units.
func segmentWithMarkers(text string, base func(string) iter.Seq[SegmentData], validIDs map[int]bool) iter.Seq[SegmentData] {
	if len(validIDs) == 0 || !strings.Contains(text, "[paste #") {
		return base(text)
	}
	type markerSpan struct {
		data SegmentData
		end  int
	}
	var markers []markerSpan
	byteOffset, index := 0, 0
	for _, match := range pasteMarkerRegex.FindAllStringSubmatchIndex(text, -1) {
		id, err := strconv.Atoi(text[match[2]:match[3]])
		if err != nil || !validIDs[id] {
			continue
		}
		index += jsstring.Length(text[byteOffset:match[0]])
		part := text[match[0]:match[1]]
		end := index + jsstring.Length(part)
		markers = append(markers, markerSpan{SegmentData{Segment: part, Index: index, Input: text}, end})
		byteOffset, index = match[1], end
	}
	if len(markers) == 0 {
		return base(text)
	}
	return func(yield func(SegmentData) bool) {
		markerIndex := 0
		for segment := range base(text) {
			for markerIndex < len(markers) && markers[markerIndex].end <= segment.Index {
				markerIndex++
			}
			if markerIndex < len(markers) {
				marker := markers[markerIndex]
				if segment.Index >= marker.data.Index && segment.Index < marker.end {
					if segment.Index == marker.data.Index && !yield(marker.data) {
						return
					}
					continue
				}
			}
			if !yield(segment) {
				return
			}
		}
	}
}

func (e *Editor) segment(text, mode string) iter.Seq[SegmentData] {
	base := graphemeSegmentData
	if mode == "word" {
		base = wordsegmenter.Segments
	}
	return segmentWithMarkers(text, base, e.validPasteIDs())
}

func (e *Editor) wordNavigationOptions() WordNavigationOptions {
	return WordNavigationOptions{
		Segment:         func(text string) iter.Seq[SegmentData] { return e.segment(text, "word") },
		IsAtomicSegment: isPasteMarker,
	}
}

func (e *Editor) prevWordStart(line string, col int) int {
	return FindWordBackward(line, col, e.wordNavigationOptions())
}

func (e *Editor) nextWordEnd(line string, col int) int {
	return FindWordForward(line, col, e.wordNavigationOptions())
}

func (e *Editor) previousSegmentStart(text string, col int) int {
	if col <= 0 {
		return 0
	}
	segments := e.segmentLine(jsstring.Slice(text, 0, col))
	if len(segments) == 0 {
		return 0
	}
	return segments[len(segments)-1].Start
}

func (e *Editor) nextSegmentEnd(text string, col int) int {
	if col >= jsstring.Length(text) {
		return jsstring.Length(text)
	}
	segments := e.segmentLine(jsstring.Slice(text, col, jsstring.Length(text)))
	if len(segments) == 0 {
		return jsstring.Length(text)
	}
	return col + segments[0].End
}
