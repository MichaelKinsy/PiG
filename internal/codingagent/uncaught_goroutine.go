package codingagent

// uncaught_goroutine.go gives a goroutine that no caller awaits the fate Pi gives an unawaited promise that rejects: the
// process's uncaughtException handler runs, which restores the terminal, prints the error, records the crash and exits 1.
// Go has no such handler. A panic in a goroutine that does not recover ends the process with the Go runtime's stderr dump
// and no crash record.
//
// pig additive (D102).

import (
	"runtime/debug"
	"sync"
	"sync/atomic"
)

var uncaughtGoroutineHandler atomic.Pointer[func(value any, stack []byte)]

// SetUncaughtGoroutineHandler installs the handler that RecoverUncaught calls and returns a function that removes it.
// The handler owns ending the process.
func SetUncaughtGoroutineHandler(handler func(value any, stack []byte)) (restore func()) {
	uncaughtGoroutineHandler.Store(&handler)
	return func() { uncaughtGoroutineHandler.CompareAndSwap(&handler, nil) }
}

// RecoverUncaught is deferred first in a goroutine that nothing awaits. Without a handler it panics again, so the Go
// runtime reports the original panic as it does for an unguarded goroutine.
func RecoverUncaught() {
	value := recover()
	if value == nil {
		return
	}
	handler := uncaughtGoroutineHandler.Load()
	if handler == nil {
		panic(value)
	}
	(*handler)(value, debug.Stack())
}

// backgroundGroup joins goroutines the interactive session starts. A panic in one reaches the uncaught handler.
type backgroundGroup struct{ sync.WaitGroup }

// Go runs task in a new goroutine that the group's Wait joins.
func (g *backgroundGroup) Go(task func()) {
	g.WaitGroup.Go(func() {
		defer RecoverUncaught()
		task()
	})
}
