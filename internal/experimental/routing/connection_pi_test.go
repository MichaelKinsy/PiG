package routing

import "testing"

// pi: packages/server/src/connection.ts

// isTerminalConnection (connection.ts:29-31): a connection is terminal when it has disconnected or its stage is closing or closed; the
// other stages are live.
func TestConnectionStateTerminalMatchesPi(t *testing.T) {
	stages := map[string]bool{"awaitingHello": false, "handshaking": false, "ready": false, "closing": true, "closed": true}
	for stage, terminalByStage := range stages {
		for _, disconnected := range []bool{false, true} {
			state := &connectionState{stage: stage, disconnected: disconnected}
			if got, want := state.terminal(), terminalByStage || disconnected; got != want {
				t.Errorf("stage %q disconnected=%v: terminal() = %v, want %v", stage, disconnected, got, want)
			}
		}
	}
}
