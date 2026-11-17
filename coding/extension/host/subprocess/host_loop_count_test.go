package subprocess

// loopCount is the number of connection read loops the Host runs: the connections goHandleIncoming registered whose loop goroutine has not ended.
func (h *Host) loopCount() int {
	h.loopsMu.Lock()
	defer h.loopsMu.Unlock()
	return len(h.loops)
}
