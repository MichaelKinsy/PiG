package widthx

import "strings"

// rgiSequenceList is rgiSequences, which the tables hold as one newline-separated string, as a slice for the tests that draw from it.
var rgiSequenceList = strings.Split(rgiSequences, "\n")
