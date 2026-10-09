package doubleclose

import "sync"

type S struct {
	done chan struct{}
	once sync.Once
	mu   sync.Mutex
}

func (s *S) Bad() {
	close(s.done) // want `close of a shared channel`
}

func (s *S) GoodOnce() { s.once.Do(func() { close(s.done) }) }

func (s *S) GoodLock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.done)
}

func local() {
	c := make(chan int)
	close(c)
}
