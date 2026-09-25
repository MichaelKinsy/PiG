package session

import "sync"

// MutationLine serializes complete read-modify-write jobs for one Session.
// Admission is synchronous and FIFO: a job admitted by Enqueue starts only
// after every earlier job settles, even when the earlier job failed. Seal
// rejects queued and future jobs while the running job drains. The zero value
// is ready to use.
type MutationLine struct {
	mu        sync.Mutex
	tail      chan struct{}
	sealedErr error
}

// LineJob is the pending result of one admitted job.
type LineJob struct {
	done  chan struct{}
	value any
	err   error
}

// Done is closed when the job settles.
func (job *LineJob) Done() <-chan struct{} { return job.done }

// Wait blocks until the job settles and returns its result.
func (job *LineJob) Wait() (any, error) {
	<-job.done
	return job.value, job.err
}

// Enqueue admits op behind every earlier job and returns without waiting. A
// sealed line rejects op with the sealing error, both at admission and when
// op reaches the head of the line.
func (line *MutationLine) Enqueue(op func() (any, error)) *LineJob {
	job := &LineJob{done: make(chan struct{})}
	line.mu.Lock()
	if line.sealedErr != nil {
		job.err = line.sealedErr
		line.mu.Unlock()
		close(job.done)
		return job
	}
	previous := line.tail
	next := make(chan struct{})
	line.tail = next
	line.mu.Unlock()
	go func() {
		defer close(next)
		if previous != nil {
			<-previous
		}
		line.mu.Lock()
		sealed := line.sealedErr
		line.mu.Unlock()
		if sealed != nil {
			job.err = sealed
		} else {
			job.value, job.err = op()
		}
		close(job.done)
	}()
	return job
}

// Run admits op and waits for its result.
func (line *MutationLine) Run(op func() (any, error)) (any, error) {
	return line.Enqueue(op).Wait()
}

// Seal rejects queued and future jobs with err (the first sealing error wins)
// and returns a channel closed once every admitted job has settled.
func (line *MutationLine) Seal(err error) <-chan struct{} {
	line.mu.Lock()
	defer line.mu.Unlock()
	if line.sealedErr == nil {
		line.sealedErr = err
	}
	if line.tail == nil {
		drained := make(chan struct{})
		close(drained)
		return drained
	}
	return line.tail
}
