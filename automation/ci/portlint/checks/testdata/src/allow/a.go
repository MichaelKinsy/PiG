package allow

import "regexp"

//portlint:allow regexfold
var noReason = regexp.MustCompile(`(?i)x`)

//portlint:allow regexfold Pi uses /i here and the pattern is ASCII
var withReason = regexp.MustCompile(`(?i)y`)

//portlint:allow all generic marker with a reason
var all = regexp.MustCompile(`(?i)z`)

//portlint:allow clock wrong check name
var wrongCheck = regexp.MustCompile(`(?i)w`) // want `\(\?i\) pattern`
