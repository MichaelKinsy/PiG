// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package invocation

import "testing"

func TestAcknowledgedReportsReleaseState(t *testing.T) {
	if Acknowledged(t.Context()) {
		t.Fatal("unbound context reported acknowledged")
	}
	called := 0
	ctx := WithAcknowledgment(t.Context(), func() { called++ })
	if Acknowledged(ctx) {
		t.Fatal("freshly bound context reported acknowledged before release")
	}
	Acknowledge(ctx)
	if !Acknowledged(ctx) || called != 1 {
		t.Fatalf("after Acknowledge: Acknowledged=%v called=%d, want true, 1", Acknowledged(ctx), called)
	}
	Acknowledge(ctx)
	if !Acknowledged(ctx) || called != 1 {
		t.Fatalf("second Acknowledge: Acknowledged=%v called=%d, want true, 1", Acknowledged(ctx), called)
	}
}

func TestAcknowledgedBySuspensionDistinguishesCompleteFromAcknowledge(t *testing.T) {
	ctxComplete := WithAcknowledgment(t.Context(), func() {})
	Complete(ctxComplete)
	if !Acknowledged(ctxComplete) {
		t.Fatal("expected Acknowledged=true after Complete")
	}
	if AcknowledgedBySuspension(ctxComplete) {
		t.Fatal("Complete must not set AcknowledgedBySuspension")
	}

	ctxAck := WithAcknowledgment(t.Context(), func() {})
	Acknowledge(ctxAck)
	if !Acknowledged(ctxAck) || AcknowledgedBySuspension(ctxAck) {
		t.Fatalf("after plain Acknowledge: Acknowledged=%v AcknowledgedBySuspension=%v, want true, false", Acknowledged(ctxAck), AcknowledgedBySuspension(ctxAck))
	}

	ctxSuspend := WithAcknowledgment(t.Context(), func() {})
	AcknowledgeSuspension(ctxSuspend)
	if !Acknowledged(ctxSuspend) || !AcknowledgedBySuspension(ctxSuspend) {
		t.Fatalf("after AcknowledgeSuspension: Acknowledged=%v AcknowledgedBySuspension=%v, want true, true", Acknowledged(ctxSuspend), AcknowledgedBySuspension(ctxSuspend))
	}
}
