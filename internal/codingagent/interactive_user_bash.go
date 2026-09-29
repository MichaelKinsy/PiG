package codingagent

import (
	"context"
	"errors"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts.
// A pending user_bash hook does not mark bash running. Its eventual execution and completion belong to the same task.
type userBashTask struct {
	ctx     context.Context
	owner   context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	running bool // owner-loop state
}

func (m *InteractiveMode) startUserBashTask(ctx context.Context, work func(*userBashTask)) {
	if m.settlingUserBash || ctx.Err() != nil || m.abortCtx.Err() != nil {
		return
	}
	owner := m.backgroundCtx
	if owner == nil {
		owner = ctx
	}
	if owner.Err() != nil {
		return
	}
	taskCtx, cancel := context.WithCancel(ctx)
	stopOwner := context.AfterFunc(owner, cancel)
	stopAbort := context.AfterFunc(m.abortCtx, cancel)
	task := &userBashTask{ctx: taskCtx, owner: owner, cancel: cancel, done: make(chan struct{})}
	if m.userBashTasks == nil {
		m.userBashTasks = make(map[*userBashTask]struct{})
	}
	m.userBashTasks[task] = struct{}{}
	m.backgroundTasks.Go(func() {
		defer close(task.done)
		defer func() {
			cancel()
			stopOwner()
			stopAbort()
			m.awaitUserBashMain(owner, func() {
				delete(m.userBashTasks, task)
				if task.running {
					for other := range m.userBashTasks {
						if other.running {
							return
						}
					}
					m.bashCancel = nil
					m.isIdle = !m.runStreaming()
				}
			})
		}()
		if taskCtx.Err() == nil {
			work(task)
		}
	})
}

// awaitUserBashMain acknowledges execution, not merely queue admission. Cancellation invalidates an already queued mutation as well as a waiting producer.
func (m *InteractiveMode) awaitUserBashMain(ctx context.Context, fn func()) bool {
	done := make(chan struct{})
	if m.postToMain(ctx, func() {
		if ctx.Err() == nil {
			fn()
		}
		close(done)
	}) != nil {
		return false
	}
	select {
	case <-done:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

func (m *InteractiveMode) cancelRunningUserBash() {
	for task := range m.userBashTasks {
		if task.running {
			task.cancel()
		}
	}
}

// settleUserBash cancels and joins outgoing work while servicing the owner loop. A callback that ignores cancellation must return before its Session or extension registry can be replaced.
func (m *InteractiveMode) settleUserBash() error {
	m.settlingUserBash = true
	defer func() { m.settlingUserBash = false }()
	for task := range m.userBashTasks {
		task.cancel()
	}
	for task := range m.userBashTasks {
		if err := m.waitUserBashTask(task); err != nil {
			return err
		}
	}
	return nil
}

func (m *InteractiveMode) waitUserBashTask(task *userBashTask) error {
	for {
		select {
		case <-task.done:
			return nil
		case <-task.owner.Done():
			return errors.New("interactive mode shut down before user bash settled")
		case fn := <-m.uiTaskCh:
			fn()
		case ev, ok := <-m.eventCh:
			if !ok {
				m.eventCh = nil
				continue
			}
			m.handleAgentEvent(ev)
		case <-m.renderWakeCh:
			m.runScheduledRender()
		}
	}
}
