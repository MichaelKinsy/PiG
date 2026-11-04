package session

// BelowRecount counts the children listed in below the slow way, to check the count IndexSizes keeps.
func (s *Scheduler) BelowRecount() int {
	n := 0
	for _, children := range s.below {
		n += children.Len()
	}
	return n
}

// Publish runs the Session's publication of a commit the test made up: the tails, the task documents and the commit listeners.
func (s *Session) Publish(pub *Publication) { s.publish(pub) }
