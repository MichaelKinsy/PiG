package regexfold

import "regexp"

var bad = regexp.MustCompile(`(?i)^bearer\s`) // want `\(\?i\) pattern`

var good = regexp.MustCompile(`^[Bb]earer\s`)
