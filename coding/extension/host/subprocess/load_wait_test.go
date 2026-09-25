package subprocess

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Upstream loader.ts awaits an extension factory with no deadline. A Node
// factory runs before the runtime connects, so a factory that takes longer
// than any fixed connect deadline must still load.
func TestSlowNodeFactoryLoads(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out a slow extension factory")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the slow-factory fixture: %v", err)
	}
	// The product deadline this replaces was 10 s, overridable only by a test
	// environment variable; clear it so the product path is what runs.
	t.Setenv("PIG_TEST_CONNECT_TIMEOUT_SEC", "")
	dir := t.TempDir()
	source := `export default async function (pi) {
  await new Promise((resolve) => setTimeout(resolve, 12000));
  pi.registerCommand("slow-ready", { description: "registered after a slow factory", handler: async () => {} });
}
`
	if err := os.WriteFile(filepath.Join(dir, "index.mjs"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: "slow-factory", Source: filepath.Join(dir, "index.mjs"), Enabled: true})
	if err != nil {
		t.Fatalf("slow factory failed to load: %v", err)
	}
	if _, ok := ext.Commands["slow-ready"]; !ok {
		t.Fatalf("slow factory registered commands %v", ext.CommandOrder)
	}
}

// After connecting, an extension that registers later than any fixed register
// deadline still loads.
func TestLateRegistrationLoads(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out a late registration")
	}
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	ext, err := host.LoadInProcess(testbudget.Context(t), ExtConfig{Name: "late-register", Enabled: true}, func(conn net.Conn) error {
		time.Sleep(6 * time.Second)
		if err := writeEnvelopeTo(conn, &Envelope{Type: MsgRegister, Register: &RegisterPayload{Name: "late-register"}}); err != nil {
			return err
		}
		for {
			env, err := readEnvelopeFrom(conn)
			if err != nil {
				return nil
			}
			if env.Type == MsgPing && env.Ping != nil {
				_ = writeEnvelopeTo(conn, &Envelope{Type: MsgPong, Pong: &PongPayload{Nonce: env.Ping.Nonce}})
			}
		}
	})
	if err != nil {
		t.Fatalf("late registration failed to load: %v", err)
	}
	if ext.Name != "late-register" {
		t.Fatalf("loaded %q", ext.Name)
	}
}

// With no deadline, the caller's context still ends a load that never
// connects, and the error names the cancellation.
func TestLoadWaitEndsWithCallerContext(t *testing.T) {
	host := NewHost(t.TempDir())
	defer host.Shutdown("test done")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := host.LoadInProcess(ctx, ExtConfig{Name: "never-registers", Enabled: true}, func(conn net.Conn) error {
		_, _ = readEnvelopeFrom(conn)
		return nil
	})
	var loadErr *LoadError
	if !errors.As(err, &loadErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("load error = %v, want the caller's cancellation", err)
	}
}
