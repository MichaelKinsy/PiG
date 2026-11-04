// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"testing"
	"unsafe"
)

// A Scheduler holds one invocation per reserved task, and U-harness-scheduler-retention's 20,000-task chain reserves them all at once,
// so what an invocation keeps for a Telemetry that a Harness may not have lives in invTel, made on first use. The ceiling is the size
// of the invocation without it, in words, on any word size.
func TestAnInvocationKeepsTheTelemetryPartOutOfLine(t *testing.T) {
	const word = unsafe.Sizeof(uintptr(0))
	if got := unsafe.Sizeof(invocation{}) / word; got > 33 {
		t.Fatalf("an invocation is %d words, over the 33 it may take: move what only a Telemetry uses to invTel", got)
	}
	inv := &invocation{}
	if inv.carrier() != nil || inv.tel != nil {
		t.Fatal("an invocation made without telemetry holds an invTel")
	}
	inv.telemetry().carrier = "carrier"
	if inv.carrier() != "carrier" || inv.telemetry() != inv.tel {
		t.Fatalf("carrier %v, telemetry %p, tel %p", inv.carrier(), inv.telemetry(), inv.tel)
	}
}
