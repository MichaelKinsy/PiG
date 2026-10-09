package coding

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"
)

// SetSessionName reaches the same collision-checked Session entry producer as Pi's appendSessionInfo.
func TestSessionSetNamePreservesCollidingEntry(t *testing.T) {
	s, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range s.Events() {
		}
	}()
	defer func() { _ = s.Close(); <-done }()
	occupied, err := s.Inner().AppendCustomEntry("seed", "retained")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Inner().GetEntry(occupied)
	var fresh string
	for i := 0; ; i++ {
		fresh = fmt.Sprintf("%08x", i)
		if _, exists := s.Inner().GetEntry(fresh); !exists {
			break
		}
	}
	entropy, err := hex.DecodeString(occupied + fresh)
	if err != nil {
		t.Fatal(err)
	}
	original := rand.Reader
	rand.Reader = bytes.NewReader(entropy)
	defer func() { rand.Reader = original }()
	if err := s.SetSessionName("renamed"); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Inner().GetEntry(occupied)
	if !bytes.Equal(before.Raw(), after.Raw()) || *s.LeafID() != fresh || s.SessionName() != "renamed" {
		t.Fatalf("name update replaced the existing entry or lost its new identity: leaf=%v name=%q", s.LeafID(), s.SessionName())
	}
}
