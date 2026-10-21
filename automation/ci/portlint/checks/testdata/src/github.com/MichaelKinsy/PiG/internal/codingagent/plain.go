package codingagent

type plainHost struct{ onEvent func(name string) }

// Other files of internal/codingagent are out of scope.
func (h *plainHost) notInScope() {
	go func() { h.onEvent("x") }()
}
