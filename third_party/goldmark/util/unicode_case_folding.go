package util

import "sync"

//go:generate go run ../_tools unicode-case-folding-map -o ../_tools/unicode-case-folding-map.json
//go:generate go run ../_tools emb-structs -i ../_tools/unicode-case-folding-map.json -o ./unicode_case_folding.gen.go

var unicodeCaseFoldings = sync.OnceValue(func() map[rune][]rune {
	foldings := make(map[rune][]rune, _unicodeCaseFoldingLength)
	cTo := 0
	for i := range _unicodeCaseFoldingLength {
		tTo := cTo + int(_unicodeCaseFoldingToIndex[i])
		to := _unicodeCaseFoldingTo[cTo:tTo]
		foldings[_unicodeCaseFoldingFrom[i]] = to
		cTo = tTo
	}
	return foldings
})
