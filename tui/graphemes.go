package tui

import "github.com/MichaelKinsy/PiG/tui/widthx"

type graphemeSegment struct {
	Text  string
	Start int
	End   int
	Width int
}

const punctuationChars = "(){}[]<>.,;:'\"!?+-=*/\\|&%^$#@~`"

func graphemeSegments(s string) []graphemeSegment {
	if s == "" {
		return nil
	}
	var segs []graphemeSegment
	from := 0
	for rest := s; rest != ""; {
		var g string
		g, rest = widthx.FirstGrapheme(rest)
		segs = append(segs, graphemeSegment{Text: g, Start: from, End: from + len(g), Width: widthx.GraphemeWidth(g)})
		from += len(g)
	}
	return segs
}
