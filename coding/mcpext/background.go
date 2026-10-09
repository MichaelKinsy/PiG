package mcpext

import "sync"

// backgroundTasks counts work that outlives the call that started it. Unlike a [sync.WaitGroup], work may start
// while another goroutine waits: a sign-in that begins during shutdown is waited for too.
type backgroundTasks struct {
	mu   sync.Mutex
	n    int
	idle chan struct{}
}

// enter registers one task. The caller calls the returned function when it finished.
func (b *backgroundTasks) enter() (exit func()) {
	b.mu.Lock()
	if b.n == 0 {
		b.idle = make(chan struct{})
	}
	b.n++
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.n--
			if b.n == 0 {
				close(b.idle)
			}
			b.mu.Unlock()
		})
	}
}

// Go runs fn on a goroutine the extension owns and drains in [Extension.SessionShutdown].
func (b *backgroundTasks) Go(fn func()) {
	exit := b.enter()
	//portlint:allow golifetime the task is registered by enter and Wait drains it, which SessionShutdown calls
	go func() {
		defer exit()
		fn()
	}()
}

// Wait returns once no task is running, including tasks that started meanwhile.
func (b *backgroundTasks) Wait() {
	for {
		b.mu.Lock()
		if b.n == 0 {
			b.mu.Unlock()
			return
		}
		idle := b.idle
		b.mu.Unlock()
		<-idle
	}
}
