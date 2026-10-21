package codingagent

type extHost struct{ onEvent func(name string) }

// ext_*.go files of internal/codingagent are in scope.
func (h *extHost) bad() {
	go func() { // want `goroutine invokes the callback h.onEvent`
		h.onEvent("x")
	}()
}
