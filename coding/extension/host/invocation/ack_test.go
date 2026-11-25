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
