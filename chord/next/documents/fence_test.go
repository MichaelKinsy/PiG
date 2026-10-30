package documents

import "testing"

// Ports packages/chord/test/next/documents/fence.test.ts

func TestSealTokensStartOpenAndStaySealedOnceSealed(t *testing.T) {
	token := NewSealToken()
	if token.Sealed() {
		t.Fatal("a new token is sealed")
	}
	token.Seal()
	token.Seal()
	if !token.Sealed() {
		t.Fatal("a sealed token is open")
	}
}
