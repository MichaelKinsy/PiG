package tui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// getImageLineExtent and stripImageSequences (Pi 787005a40e) over random lines, with the same images registered on both sides: Kitty
// commands with and without `c=` and `r=`, of registered and unregistered images, chunked, unterminated and with an escape sequence
// in the body; iTerm2 sequences with a cell, auto, pixel, percent or no width, unterminated, behind a cursor-up prefix; text, wide
// characters and SGR codes around them. Pi matches regular expressions where PiG searches bytes. A count at or beyond
// imageExtentUnbounded, Pi's Infinity included, is compared as imageExtentUnbounded.
func TestImageLineExtentAndStripMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(787005))
	type meta struct{ ImageID, Columns, Rows, WidthPx, HeightPx int }
	type input struct {
		Metas []meta
		Lines []string
		Cap   int
	}
	in := input{Cap: imageExtentUnbounded}
	for id := 1; id <= 6; id++ {
		in.Metas = append(in.Metas, meta{ImageID: id, Columns: []int{0, 3, 40}[rng.Intn(3)], Rows: []int{0, 1, 5}[rng.Intn(3)], WidthPx: 100, HeightPx: 100})
	}
	pick := func(list ...string) string { return list[rng.Intn(len(list))] }
	count := func() string {
		return pick("0", "1", "4", "007", "12", "250", fmt.Sprint(rng.Intn(40)), "1073741823", "1073741824", "1073741825", "99999999999999999999999")
	}
	rows := func() string {
		return pick("0", "1", "3", "012", fmt.Sprint(rng.Intn(9)), "1073741823", "1073741824", "1073741826")
	}
	id := func() string { return fmt.Sprint(1 + rng.Intn(9)) }
	text := func() string {
		return pick("", "", "ab", " ", "日本", "é😀", "\x1b[31mred\x1b[0m", "width=3;", ";width=12;", "=width=8:", "c=5,r=2", "A", "[2A", "\x1b[")
	}
	cursorUp := func() string { return pick("", "", "\x1b[2A", "\x1b["+count()+"A", "\x1b[A", "\x1b[3B", "\x1b[2Ax") }
	kitty := func() string {
		controls := pick(
			"a=T,f=100,q=2,C=1,c="+count()+",r="+rows()+",i="+id(),
			"a=T,f=100,q=2,i="+id(),
			"a=T,r="+rows()+",i="+id(),
			"a=T,c="+count()+",i="+id(),
			"c="+count(),
			"i="+id()+",c="+count()+",r="+rows(),
			"a=p,ac="+count()+",i="+id(),
			"a=T,,c="+count()+",,r="+rows(),
			"a=T,c=,r=,i="+id(),
			"a=T,c=-3,r=-1,i="+id(),
			"",
		)
		return "\x1b_G" + controls + pick(
			";AAAA\x1b\\",
			";AAAA\x1b\\",
			",m=1;AAAA\x1b\\\x1b_Gm=0;BBBB\x1b\\",
			";AA\x1b[0mBB\x1b\\",
			";AAAA",
			"\x1b\\",
			"",
			";AA;c=9,r=9\x1b\\",
		)
	}
	iterm2 := func() string {
		params := pick(
			"inline=1;size=3;width="+count()+";height=auto",
			"inline=1;width="+count(),
			"width="+count()+";inline=1",
			"inline=1;size=3;width=auto;height=auto",
			"inline=1;width="+count()+"%",
			"inline=1;width="+count()+"px",
			"inline=1;xwidth="+count(),
			"inline=1;size=3",
			"inline=1;width=;height=2",
			"inline=1;height="+count()+";width="+count(),
		)
		return cursorUp() + "\x1b]1337;File=" + params + pick(":AAAA\x07", ":AAAA\x07", ":AAAA", "\x07", ":AA=width=6:\x07", "")
	}
	for range 6000 {
		var line strings.Builder
		line.WriteString(text())
		for range rng.Intn(4) {
			if rng.Intn(2) == 0 {
				line.WriteString(kitty())
			} else {
				line.WriteString(iterm2())
			}
			line.WriteString(text())
			line.WriteString(cursorUp())
		}
		in.Lines = append(in.Lines, line.String())
	}
	type extent struct {
		Found                               bool
		FirstRow, LastRow, StartCol, EndCol int
	}
	type result struct {
		Extent []extent `json:"extent"`
		Strip  []string `json:"strip"`
	}
	var want result
	pioracle.Run(t, `
const m = await load("pi-tui/terminal-image.js");
for (const x of input.Metas) m.registerKittyImageMetadata({ imageId: x.ImageID, columns: x.Columns, rows: x.Rows, widthPx: x.WidthPx, heightPx: x.HeightPx });
const cap = (n) => Math.max(-input.Cap, Math.min(input.Cap, n)) + 0;
emit({
	extent: input.Lines.map((l) => { const e = m.getImageLineExtent(l); return e ? { Found: true, FirstRow: cap(e.firstRow), LastRow: cap(e.lastRow), StartCol: cap(e.startCol), EndCol: cap(e.endCol) } : { Found: false }; }),
	strip: input.Lines.map((l) => m.stripImageSequences(l)),
});
`, in, &want)

	resetKittyRegistry(t)
	t.Cleanup(func() { resetKittyRegistry(t) })
	for _, x := range in.Metas {
		RegisterKittyImageMetadata(KittyImageMetadata(x))
	}
	bad, found := 0, 0
	for i, line := range in.Lines {
		e, ok := getImageLineExtent(line)
		if got := (extent{Found: ok, FirstRow: e.firstRow, LastRow: e.lastRow, StartCol: e.startCol, EndCol: e.endCol}); got != want.Extent[i] {
			if bad++; bad <= 10 {
				t.Errorf("extent of %q:\n got %+v\nwant %+v", line, got, want.Extent[i])
			}
		}
		if ok {
			found++
		}
		if got := stripImageSequences(line); got != want.Strip[i] {
			if bad++; bad <= 10 {
				t.Errorf("strip of %q:\n got %q\nwant %q", line, got, want.Strip[i])
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d of %d results differ from Pi", bad, 2*len(in.Lines))
	}
	if found < len(in.Lines)/2 {
		t.Fatalf("only %d of %d lines are image lines: the generator no longer exercises the extent", found, len(in.Lines))
	}
}
