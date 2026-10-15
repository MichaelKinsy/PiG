package tools

import (
	"io"
	"math"
	"unicode/utf8"
)

const (
	// pig divergence (D151): grepWindowChunk is the read size of the line window; Pi reads whole files.
	grepWindowChunk = 64 << 10
	// grepStoredLineRunes bounds a stored line. grep prints a line cut to GrepMaxLineLength UTF-16 units plus a notice when it is longer, so the first GrepMaxLineLength+1 code points decide the same output as the whole line.
	grepStoredLineRunes = GrepMaxLineLength + 1
)

// pig divergence (D151): grepLineWindow reads a file's lines in order and keeps the lines a later match can still need,
// so the context of consecutive matches is served without holding the
// file. Lines split as upstream getFileLines splits them: the text is decoded
// as UTF-8, then "\r\n", "\r" and "\n" each end a line, and the text after the
// last separator is a line, even when empty.
type grepLineWindow struct {
	open      func() (io.ReadCloser, error)
	src       io.ReadCloser
	started   bool
	done      bool
	failed    bool
	decoder   utf8StreamDecoder
	pendingCR bool
	chunk     []byte
	partial   []byte
	runes     int
	// next is the number of lines read; lines holds the last len(lines) of them that are numbered keepFrom or higher.
	next     int
	keepFrom int
	lines    []string
}

// countAtLeast reads until n lines exist or the file ends and returns the
// number of lines read so far, counted from the first line of the file. It
// returns 0 when the file cannot be read.
func (w *grepLineWindow) countAtLeast(n float64) int {
	for !w.done && !w.failed && float64(w.next) < n {
		w.fill()
	}
	if w.failed {
		return 0
	}
	return w.next
}

// line returns line n when the window holds it.
func (w *grepLineWindow) line(n int) string {
	if i := n - (w.next - len(w.lines) + 1); i >= 0 && i < len(w.lines) {
		return w.lines[i]
	}
	return ""
}

// discardBefore drops the lines above first, which no later match of the file
// needs, and stops keeping them as the file is read.
func (w *grepLineWindow) discardBefore(first float64) {
	keep := int(math.Min(math.Floor(first), math.MaxInt32))
	if keep <= w.keepFrom {
		return
	}
	w.keepFrom = keep
	if drop := keep - (w.next - len(w.lines) + 1); drop > 0 {
		// Shift the kept lines down in place; clearing the vacated tail releases the dropped strings.
		kept := copy(w.lines, w.lines[min(drop, len(w.lines)):])
		clear(w.lines[kept:])
		w.lines = w.lines[:kept]
	}
}

func (w *grepLineWindow) close() {
	if w.src != nil {
		_ = w.src.Close()
		w.src = nil
	}
}

// fill reads one chunk, or finishes the file.
func (w *grepLineWindow) fill() {
	if !w.started {
		w.started = true
		src, err := w.open()
		if err != nil {
			w.failed = true
			return
		}
		w.src = src
	}
	if w.chunk == nil {
		w.chunk = make([]byte, grepWindowChunk)
	}
	n, err := w.src.Read(w.chunk)
	if n > 0 {
		w.add(w.decoder.decode(w.chunk[:n], true))
	}
	switch err {
	case nil:
	case io.EOF:
		w.add(w.decoder.decode(nil, false))
		w.endLine()
		w.done = true
		w.close()
	default:
		w.failed = true
		w.close()
	}
}

func (w *grepLineWindow) add(text string) {
	for _, r := range text {
		switch r {
		case '\r':
			w.endLine()
			w.pendingCR = true
			continue
		case '\n':
			if !w.pendingCR {
				w.endLine()
			}
		default:
			if w.runes < grepStoredLineRunes && w.next+1 >= w.keepFrom {
				w.partial = utf8.AppendRune(w.partial, r)
			}
			w.runes++
		}
		w.pendingCR = false
	}
}

func (w *grepLineWindow) endLine() {
	w.next++
	if w.next >= w.keepFrom {
		w.lines = append(w.lines, string(w.partial))
	}
	w.partial = w.partial[:0]
	w.runes = 0
}
