//go:build !chordstall

package parking

// wait is Wait: nothing but the wait, no timer, no allocation.
func (s *Signal) wait() { <-s.done }
