package subprocess

// queueCall is a test helper that calls queueCallDone with ackCounted=false and a nil done callback.
func (h *Host) queueCall(me *managedExt, conn *Conn, lanes *callLanes, callID string, call *CallPayload) {
	h.queueCallDone(me, conn, lanes, callID, call, false, nil, nil)
}
