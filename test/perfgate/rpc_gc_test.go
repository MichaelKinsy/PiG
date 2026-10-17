// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import "testing"

// D175: with GOGC unset, an RPC run collects at GOGC 400, as print and json runs do. The process reports the GC percent it exits at; at the default it would be 100. Red without the rpc case in cli.Main: percent 100.
func TestRPCModeRaisesGCTarget(t *testing.T) {
	home := newPigHome(t)
	args := []string{"-ne", "--no-session", "--mode", "rpc", "--model", "test-faux/faux-1"}
	home.run(args, nil) // The first start in a new home materializes caches.
	if record := home.run(args, rpcTurns(t, 2)); record.GCPercent != 400 {
		t.Errorf("rpc run with GOGC unset exited at GC percent %d, want 400 (%+v)", record.GCPercent, record)
	}
}

// D175: a user's GOGC wins over the rpc target.
func TestRPCModeKeepsUsersGOGC(t *testing.T) {
	home := newPigHome(t)
	home.env = append(home.env, "GOGC=100")
	args := []string{"-ne", "--no-session", "--mode", "rpc", "--model", "test-faux/faux-1"}
	home.run(args, nil)
	if record := home.run(args, rpcTurns(t, 2)); record.GCPercent != 100 {
		t.Errorf("rpc run with GOGC=100 exited at GC percent %d, want 100 (%+v)", record.GCPercent, record)
	}
}
