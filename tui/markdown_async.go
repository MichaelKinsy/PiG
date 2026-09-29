package tui

import (
	"container/list"
	"context"
	"sync"
)

// AsyncMarkdownTransform prepares an owned, cancellable display rewrite. Prepare runs on the UI loop and snapshots mutable inputs; its returned function runs off-loop. Start joins that work at owner shutdown. Invalidate requests a paint without mutating UI state.
// pig additive (D19): subprocess callbacks execute outside the host render loop.
type AsyncMarkdownTransform struct {
	Context    context.Context
	Start      func(func())
	Invalidate func()
	Prepare    func(markdown string, width int) func(context.Context) string
	// Queue preserves render admission order across components belonging to one owner. A nil queue keeps work local to the component.
	Queue *MarkdownTransformQueue
}

type markdownTransformInput struct {
	text     string
	width    int
	state    string
	theme    *Theme
	revision uint64
}

type markdownTransformJob struct {
	input   markdownTransformInput
	run     func(context.Context) string
	options *AsyncMarkdownTransform
	state   *asyncMarkdownState
	ctx     context.Context
	cancel  context.CancelFunc
}

// MarkdownTransformQueue serializes individual transform generations in render admission order. Each component retains at most one queued replacement in addition to the owner's active callback.
type MarkdownTransformQueue struct {
	mu      sync.Mutex
	jobs    list.List
	pending map[*asyncMarkdownState]*list.Element
	running bool
}

func (q *MarkdownTransformQueue) submit(job *markdownTransformJob) {
	q.mu.Lock()
	if q.pending == nil {
		q.pending = make(map[*asyncMarkdownState]*list.Element)
	}
	if previous := q.pending[job.state]; previous != nil {
		q.jobs.Remove(previous)
	}
	q.pending[job.state] = q.jobs.PushBack(job)
	start := !q.running
	q.running = true
	q.mu.Unlock()
	if start {
		job.options.Start(q.work)
	}
}

func (q *MarkdownTransformQueue) remove(state *asyncMarkdownState) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if queued := q.pending[state]; queued != nil {
		q.jobs.Remove(queued)
		delete(q.pending, state)
	}
}

func (q *MarkdownTransformQueue) work() {
	for {
		q.mu.Lock()
		front := q.jobs.Front()
		if front == nil {
			q.running = false
			q.mu.Unlock()
			return
		}
		job := front.Value.(*markdownTransformJob)
		q.jobs.Remove(front)
		delete(q.pending, job.state)
		q.mu.Unlock()
		job.execute()
	}
}

func (job *markdownTransformJob) execute() {
	defer job.cancel()
	if job.ctx.Err() != nil {
		return
	}
	result := job.run(job.ctx)
	s := job.state
	s.mu.Lock()
	publish := job.ctx.Err() == nil && s.current == job
	if publish {
		s.result, s.ready = result, true
	}
	s.mu.Unlock()
	if publish && job.options.Invalidate != nil {
		job.options.Invalidate()
	}
}

type asyncMarkdownState struct {
	mu       sync.Mutex
	current  *markdownTransformJob
	result   string
	ready    bool
	local    MarkdownTransformQueue
	disposed bool
}

// resolve supersedes the logical generation without detaching its worker. The owner decides how callback completion relates to generation cancellation.
func (s *asyncMarkdownState) resolve(options *AsyncMarkdownTransform, input markdownTransformInput) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disposed || options.Context.Err() != nil {
		return "", false
	}
	if s.current != nil && s.current.input == input && s.current.options == options {
		return s.result, s.ready
	}
	if s.current != nil {
		s.current.cancel()
	}
	ctx, cancel := context.WithCancel(options.Context)
	job := &markdownTransformJob{input: input, run: options.Prepare(input.text, input.width), options: options, state: s, ctx: ctx, cancel: cancel}
	s.current, s.ready = job, false
	queue := options.Queue
	if queue == nil {
		queue = &s.local
	}
	queue.submit(job)
	return "", false
}

func (s *asyncMarkdownState) dispose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disposed = true
	s.ready = false
	if s.current != nil {
		s.current.cancel()
		queue := s.current.options.Queue
		if queue == nil {
			queue = &s.local
		}
		queue.remove(s)
		s.current = nil
	}
}
