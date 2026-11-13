package testenv

import (
	"regexp"
	"strings"
)

// bubbleLabel is how the header of a goroutine in a stack dump names the synctest bubble the goroutine runs in.
var bubbleLabel = regexp.MustCompile(`, synctest bubble \d+\b`)

// LiveGoroutines counts the goroutines of the caller's synctest bubble that a stack dump lists; outside a bubble it
// counts every goroutine. A test that compares the count before and after synctest.Wait uses it instead of
// runtime.NumGoroutine, which still counts, for a moment, a goroutine that has returned and that synctest.Wait already
// reported gone, and which counts the whole process: a goroutine outside the bubble, such as the frame timer of
// another test's renderer that fires between the two counts, is not the work of the code under test.
func LiveGoroutines() int {
	var bubble string
	n := 0
	for i, g := range liveGoroutines() {
		header, _, _ := strings.Cut(g.stack, "\n")
		label := bubbleLabel.FindString(header)
		if i == 0 {
			// runtime.Stack lists the calling goroutine first.
			bubble = label
		}
		if bubble == "" || label == bubble {
			n++
		}
	}
	return n
}
