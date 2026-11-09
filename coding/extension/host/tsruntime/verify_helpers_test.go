package tsruntime

import "context"

// verifyClosed is Verify for a test that only looks at the outcome: it releases the verified executable at once.
func verifyClosed(v *Verifier, ctx context.Context, m Manifest, executable, digest string) (Reason, error) {
	exe, reason, err := v.Verify(ctx, m, executable, digest)
	_ = exe.Close()
	return reason, err
}
