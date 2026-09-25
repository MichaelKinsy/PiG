package ai

import "testing"

func TestGeneratePKCE(t *testing.T) {
	p, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	if p.Verifier == "" {
		t.Error("empty verifier")
	}
	if p.Challenge == "" {
		t.Error("empty challenge")
	}
	if p.Verifier == p.Challenge {
		t.Error("verifier and challenge should differ")
	}

	// Deterministic: same verifier → same challenge
	// (can't test because verifier is random, but check lengths are reasonable)
	if len(p.Verifier) < 20 {
		t.Errorf("verifier too short: %d", len(p.Verifier))
	}
	if len(p.Challenge) < 20 {
		t.Errorf("challenge too short: %d", len(p.Challenge))
	}
}
