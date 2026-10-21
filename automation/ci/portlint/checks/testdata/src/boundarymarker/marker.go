package boundarymarker

type markerHost struct{ onEvent func(name string) }

func (h *markerHost) noReason() {
	//portlint:allow boundarygoroutine
	go func() { h.onEvent("x") }()
}

func (h *markerHost) noClass() {
	//portlint:allow boundarygoroutine the goroutine is joined before the handler returns
	go func() { h.onEvent("x") }()
}

func (h *markerHost) withClass() {
	//portlint:allow boundarygoroutine joined: the handler waits for this goroutine before it returns
	go func() { h.onEvent("x") }()
}

func (h *markerHost) withUpstream() {
	//portlint:allow boundarygoroutine upstream: runner.ts:emit Pi does not order these listeners
	go func() { h.onEvent("x") }()
}

func (h *markerHost) withDivergence() {
	//portlint:allow boundarygoroutine pig divergence (D177): invocation start order is unspecified
	go func() { h.onEvent("x") }()
}

func (h *markerHost) wrongClass() {
	//portlint:allow boundarygoroutine because it is fine
	go func() { h.onEvent("x") }()
}
