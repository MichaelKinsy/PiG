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

// Mint is Tx.mint: the next ID, noted as Pi's Storage mints it `after` ticks into a method that began at tick `from` of the callback.
func (tx *Tx) Mint(from, after int) ID { return tx.mint(from, after) }

// StorageNextID is the next ID Pi's Storage would mint now.
func (s *Session) StorageNextID() ID { return s.storageNextID() }
