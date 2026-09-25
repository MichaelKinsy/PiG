package experimental

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func echoBackend(t *testing.T, path, greeting string) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	accepted := make(chan net.Conn, 1)
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		accepted <- conn
		defer func() { _ = conn.Close() }()
		if _, err := io.WriteString(conn, greeting); err != nil {
			return
		}
		_, _ = io.Copy(conn, conn)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		// Wait for Accept to either fail or publish its connection before joining the echo.
		select {
		case conn := <-accepted:
			_ = conn.Close()
		case <-done:
		}
		<-done
	})
}

func TestCoordinatorPublicGenerationReplacement(t *testing.T) {
	for _, oracle := range []bool{true, false} {
		t.Run(fmt.Sprintf("upstream=%t", oracle), func(t *testing.T) {
			public, control, _ := startTestCoordinator(t, oracle)
			backend1, backend2 := public+"1", public+"2"
			echoBackend(t, backend1, "one\n")
			echoBackend(t, backend2, "two\n")
			first := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: backend1})
			defer first.Close()
			if err := first.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			client := controlDial(t, public)
			if greeting, err := client.reader.ReadString('\n'); err != nil || greeting != "one\n" {
				t.Fatalf("greeting %q %v", greeting, err)
			}
			payload := bytes.Repeat([]byte{0, 1, 2, 255, 10}, 128*1024)
			written := make(chan error, 1)
			go func() { _, err := client.Write(payload); written <- err }()
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(client.reader, got); err != nil {
				t.Fatal(err)
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatal("public relay changed opaque bytes")
			}
			second := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: backend2})
			defer second.Close()
			if err := second.Connect(t.Context()); err != nil {
				t.Fatal(err)
			}
			client.wantClosed(t)
			next := controlDial(t, public)
			if greeting, err := next.reader.ReadString('\n'); err != nil || greeting != "two\n" {
				t.Fatalf("next greeting %q %v", greeting, err)
			}
		})
	}
}

func TestCoordinatorSocketOwnership(t *testing.T) {
	t.Run("regular file", func(t *testing.T) {
		dir := t.TempDir()
		public, control := filepath.Join(dir, "p"), filepath.Join(dir, "c")
		if err := os.WriteFile(control, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := startCoordinator(public, control); err == nil {
			t.Fatal("overwrote regular file")
		}
		data, err := os.ReadFile(control)
		if err != nil || string(data) != "keep" {
			t.Fatalf("file changed: %q %v", data, err)
		}
	})
	t.Run("live owner", func(t *testing.T) {
		public, control, _ := startTestCoordinator(t, false)
		if _, err := startCoordinator(public, control); err == nil {
			t.Fatal("accepted live coordinator socket")
		}
		lease := controlDial(t, control)
		lease.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "alive"})
		lease.want(t, `{"type":"peer_registered","peerId":"alive"}`)
	})
	t.Run("stale sockets", func(t *testing.T) {
		dir := t.TempDir()
		public, control := filepath.Join(dir, "p"), filepath.Join(dir, "c")
		for _, path := range []string{public, control} {
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			listener.SetUnlinkOnClose(false)
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
		}
		c, err := startCoordinator(public, control)
		if err != nil {
			t.Fatal(err)
		}
		c.shutdown()
		<-c.done
		if c.err != nil {
			t.Fatal(c.err)
		}
		for _, path := range []string{public, control} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cleanup %s: %v", path, err)
			}
		}
	})
	t.Run("second listen fails", func(t *testing.T) {
		dir := t.TempDir()
		control := filepath.Join(dir, "c")
		if _, err := startCoordinator(filepath.Join(dir, "absent", "p"), control); err == nil {
			t.Fatal("missing directory accepted")
		}
		if _, err := os.Lstat(control); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("failed startup retained control socket: %v", err)
		}
	})
}

func TestCoordinatorStartupLease(t *testing.T) {
	public, control, done := startTestCoordinator(t, false)
	lease, err := EnsureCoordinator(t.Context(), public, control)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	peer := controlDial(t, control)
	peer.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "temporary"})
	peer.want(t, `{"type":"peer_registered","peerId":"temporary"}`)
	_ = peer.Close()
	select {
	case <-done:
		t.Fatal("unregistered startup lease did not hold ownership")
	case <-time.After(2 * emptyShutdownGrace):
	}
	lease.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("lease release did not permit shutdown")
	}
}

func TestEnsureCoordinatorSpawnsNativeEntry(t *testing.T) {
	dir := t.TempDir()
	public, control := filepath.Join(dir, "p"), filepath.Join(dir, "c")
	lease, err := EnsureCoordinator(t.Context(), public, control, InternalProcessSpawnOptions{Env: map[string]string{"PIG_TEST_COORDINATOR": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	server := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: public + ".backend"})
	if err := server.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	lease.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Lstat(control)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("spawned coordinator did not clean up: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCoordinatorCanceledTimerCannotRetireNewGeneration(t *testing.T) {
	dir := t.TempDir()
	c, err := startCoordinator(filepath.Join(dir, "p"), filepath.Join(dir, "c"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.shutdown(); <-c.done })
	c.mu.Lock()
	c.cancelEmptyShutdown()
	c.scheduleEmptyShutdown(time.Millisecond)
	// Let the expired callback wait on the router lock, then replace its timer.
	time.Sleep(50 * time.Millisecond)
	c.cancelEmptyShutdown()
	c.scheduleEmptyShutdown(time.Hour)
	c.mu.Unlock()
	select {
	case <-c.done:
		t.Fatal("canceled timer retired the replacement timer's generation")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCoordinatorEntryRejectsNonInternalRole(t *testing.T) {
	t.Setenv(InternalProcessEnv, "server")
	if err := RunCoordinatorEntry(t.Context(), nil); err == nil || err.Error() != "Coordinator entrypoint requires an internal coordinator invocation" {
		t.Fatal(err)
	}
	if _, present := os.LookupEnv(InternalProcessEnv); present {
		t.Fatal("rejected role was not consumed")
	}
	t.Setenv(InternalProcessEnv, "coordinator")
	if err := RunCoordinatorEntry(context.Background(), nil); err == nil || err.Error() != "Coordinator requires public and control socket paths" {
		t.Fatal(err)
	}
}
