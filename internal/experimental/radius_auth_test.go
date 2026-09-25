package experimental

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

const testConnectionID = "00000000-0000-4000-8000-000000000002"
const testServerID = "00000000-0000-4000-8000-000000000001"

// The pinned experimental-radius-relay.test.ts requires this exact multiplexing envelope.
func TestRelayDataFrameEnvelope(t *testing.T) {
	payload := []byte{0, 1, 2, 255}
	frame, err := EncodeRelayDataFrame(testConnectionID, payload)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 1, 0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 2, 0, 1, 2, 255}
	if !bytes.Equal(frame, want) {
		t.Fatalf("frame = %x, want %x", frame, want)
	}
	parsed, ok := ParseRelayDataFrame(frame)
	if !ok || parsed.ConnectionID != testConnectionID || !bytes.Equal(parsed.Payload, payload) {
		t.Fatalf("parsed = %+v, %v", parsed, ok)
	}
	frame[18] = 99
	if parsed.Payload[0] != 0 {
		t.Fatal("parsed payload aliases frame")
	}
	payload[0] = 88
	if frame[18] == 88 {
		t.Fatal("encoded payload aliases input")
	}
	for _, invalid := range [][]byte{nil, want[:17], append([]byte{2}, want[1:]...), append([]byte{1, 2}, want[2:]...)} {
		if _, ok := ParseRelayDataFrame(invalid); ok {
			t.Fatalf("accepted %x", invalid)
		}
	}
	for _, id := range []string{"", strings.ToUpper("abcdef00-0000-4000-8000-000000000002"), "00000000-0000-1000-8000-000000000002", "00000000-0000-4000-7000-000000000002"} {
		if _, err := EncodeRelayDataFrame(id, nil); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	empty, err := EncodeRelayDataFrame(testConnectionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := ParseRelayDataFrame(empty); !ok || len(p.Payload) != 0 {
		t.Fatalf("empty = %+v %v", p, ok)
	}
}

func TestRadiusAuthExplicitRereadOfflineAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("\ufeff secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Input: &AuthInput{Type: "file", Path: path}, Gateway: "localhost:8000///"})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"first", "rotated"} {
		if err := os.WriteFile(path, []byte("\ufeff "+token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		auth, err := resolver.Resolve(t.Context(), true)
		if err != nil || auth == nil || auth.Token != token || auth.Gateway != "https://localhost:8000" {
			t.Fatalf("resolve = %+v, %v", auth, err)
		}
	}
	if err := os.WriteFile(path, []byte(" \ufeff\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(t.Context(), false); err == nil || err.Error() != "Radius authentication token must not be empty" {
		t.Fatalf("empty: %v", err)
	}
	t.Setenv("PI_OFFLINE", "")
	if auth, err := resolver.Resolve(t.Context(), false); auth != nil || err != nil {
		t.Fatalf("offline optional: %+v %v", auth, err)
	}
	if _, err := resolver.Resolve(t.Context(), true); err == nil || err.Error() != "Radius relay connections are unavailable in offline mode" {
		t.Fatalf("offline required: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolver.Resolve(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must precede offline: %v", err)
	}
}

func TestRadiusAuthLazyRuntimeReadsCredentialsEveryAttempt(t *testing.T) {
	store := ai.NewInMemoryAuthStorage(nil)
	calls := 0
	resolver, err := NewRadiusRelayAuthResolver(RadiusRelayAuthOptions{Gateway: "http://localhost", CreateRuntime: func() (*codingagent.RequestAuthRuntime, error) {
		calls++
		return codingagent.NewRequestAuthRuntime(t.Context(), codingagent.RequestAuthRuntimeOptions{Credentials: store})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("runtime created eagerly")
	}
	if auth, err := resolver.Resolve(t.Context(), false); err != nil || auth != nil {
		t.Fatalf("missing: %+v %v", auth, err)
	}
	if _, err := resolver.Resolve(t.Context(), true); err == nil || !strings.Contains(err.Error(), "/login radius") {
		t.Fatalf("required: %v", err)
	}
	for _, token := range []string{"one", "two"} {
		_, err := store.Modify(t.Context(), "radius", func(*ai.Credential) (*ai.Credential, error) {
			return &ai.Credential{Type: ai.CredentialAPIKey, Key: token}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		auth, err := resolver.Resolve(t.Context(), true)
		if err != nil || auth == nil || auth.Token != token {
			t.Fatalf("stored: %+v %v", auth, err)
		}
	}
	if calls != 1 {
		t.Fatalf("runtime creations = %d", calls)
	}
}

func BenchmarkRelayDataFrame(b *testing.B) {
	payload := bytes.Repeat([]byte{42}, 64*1024)
	b.ReportAllocs()
	for b.Loop() {
		frame, err := EncodeRelayDataFrame(testConnectionID, payload)
		if err != nil {
			b.Fatal(err)
		}
		if _, ok := ParseRelayDataFrame(frame); !ok {
			b.Fatal("invalid frame")
		}
	}
}
