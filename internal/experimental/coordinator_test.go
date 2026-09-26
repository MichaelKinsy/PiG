package experimental

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("PIG_TEST_COORDINATOR") == "paused" {
		if err := os.WriteFile(os.Getenv("PIG_TEST_CHILD_READY"), []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		time.Sleep(time.Hour)
		os.Exit(1)
	}
	if os.Getenv("PIG_TEST_COORDINATOR") == "1" {
		if err := RunCoordinatorEntry(context.Background(), os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type controlTestSocket struct {
	net.Conn
	reader *bufio.Reader
}

func controlDial(t *testing.T, path string) *controlTestSocket {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &controlTestSocket{conn, bufio.NewReader(conn)}
}

func (s *controlTestSocket) send(t *testing.T, message any) {
	t.Helper()
	line, err := EncodeControlLine(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(s, line); err != nil {
		t.Fatal(err)
	}
}

func (s *controlTestSocket) want(t *testing.T, want string) {
	t.Helper()
	if err := s.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := s.reader.ReadString('\n')
	if err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	if line != want+"\n" {
		t.Fatalf("got %s want %s", line, want)
	}
}

func (s *controlTestSocket) wantClosed(t *testing.T) {
	t.Helper()
	if err := s.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err := s.reader.ReadByte()
	if err == nil {
		t.Fatal("socket sent data instead of closing")
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		t.Fatalf("socket did not close: %v", err)
	}
}

func startTestCoordinator(t *testing.T, oracle bool) (string, string, <-chan struct{}) {
	t.Helper()
	// Unix addresses must remain below the platform's sun_path length.
	dir := socketDir(t)
	public, control := filepath.Join(dir, "p"), filepath.Join(dir, "c")
	var done <-chan struct{}
	if oracle {
		if runtime.GOOS == "windows" {
			// Upstream's coordinator listens on the file paths it is given
			// (coordinator.ts listen), and Node on Windows listens only on
			// named pipes, failing with EACCES. There is no upstream
			// coordinator to compare with on Windows.
			t.Skip("upstream's experimental coordinator cannot listen on Windows: Node listens only on named pipes, not socket file paths")
		}
		root, err := filepath.Abs("../..")
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("node", "--import", upstreamLoaderImport(root), filepath.Join(root, ".upstream/current/packages/coding-agent/src/experimental/coordinator-entry.ts"), public, control)
		cmd.Env = append(os.Environ(), InternalProcessEnv+"=coordinator", "PIG_TEST_ROOT="+root, "HOME="+dir, "XDG_CONFIG_HOME="+dir, "PI_CODING_AGENT_DIR="+dir)
		var output strings.Builder
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		done = exited
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			<-exited
			if t.Failed() {
				t.Log(output.String())
			}
		})
	} else {
		child, err := SpawnInternalProcess("coordinator", []string{public, control}, InternalProcessSpawnOptions{Env: map[string]string{"PIG_TEST_COORDINATOR": "1", "HOME": dir, "XDG_CONFIG_HOME": dir, "PI_CODING_AGENT_DIR": dir}})
		if err != nil {
			t.Fatal(err)
		}
		done = child.Done()
		t.Cleanup(func() {
			if err := TerminateInternalProcess(child); err != nil {
				t.Error(err)
			}
		})
	}
	// Wait until the coordinator accepts and closes a public connection, as
	// both do while no server is registered. A dial alone succeeds once the
	// socket listens, before the accept loop runs; closing that probe
	// ourselves would leave it in the backlog, where a slow coordinator could
	// accept it after a test registers a server and relay it there.
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.Dial("unix", public)
		if err == nil {
			_ = conn.SetReadDeadline(deadline)
			_, err = conn.Read(make([]byte, 1))
			_ = conn.Close()
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			t.Fatal("coordinator did not close the startup probe")
		}
		select {
		case <-done:
			t.Fatal("coordinator exited during startup")
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	return public, control, done
}

// The same frames drive the exact pinned TypeScript entry and the native entry.
// coordinator.ts keeps workers attached across generations and ignores replaced-server traffic.
func TestCoordinatorGenerationRouting(t *testing.T) {
	for _, oracle := range []bool{true, false} {
		t.Run(fmt.Sprintf("upstream=%t", oracle), func(t *testing.T) {
			public, control, done := startTestCoordinator(t, oracle)
			for _, path := range []string{public, control} {
				// Both coordinators restrict the socket with chmod 0600
				// except on Windows, which has no POSIX mode bits.
				// coordinator.ts listens, then chmods asynchronously, so a
				// connection can be accepted just before the mode changes.
				deadline := time.Now().Add(10 * time.Second)
				for {
					info, err := os.Stat(path)
					if err == nil && (runtime.GOOS == "windows" || info.Mode().Perm() == 0o600) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("socket permissions: %v %v", info, err)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			controlDial(t, public).wantClosed(t)
			peerB := controlDial(t, control)
			peerB.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "b"})
			peerB.want(t, `{"type":"peer_registered","peerId":"b"}`)
			peerA := controlDial(t, control)
			peerA.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "a"})
			peerA.want(t, `{"type":"peer_registered","peerId":"a"}`)
			first := controlDial(t, control)
			first.send(t, map[string]any{"type": "register_server", "protocol": CoordinatorProtocolVersion, "serverConnectionId": "first", "endpoint": public + ".first"})
			first.want(t, `{"type":"server_registered","serverConnectionId":"first","peers":["b","a"]}`)
			peerB.want(t, `{"type":"server_connected","serverConnectionId":"first"}`)
			peerA.want(t, `{"type":"server_connected","serverConnectionId":"first"}`)
			peerA.send(t, map[string]any{"type": "send", "to": "server", "payload": map[string]any{"opaque": []any{nil, "é😀", 2}}})
			first.want(t, `{"type":"message","from":"a","payload":{"opaque":[null,"é😀",2]}}`)
			second := controlDial(t, control)
			second.send(t, map[string]any{"type": "register_server", "protocol": CoordinatorProtocolVersion, "serverConnectionId": "second", "endpoint": public + ".second"})
			second.want(t, `{"type":"server_registered","serverConnectionId":"second","peers":["b","a"]}`)
			first.want(t, `{"type":"server_replaced"}`)
			for _, peer := range []*controlTestSocket{peerB, peerA} {
				peer.want(t, `{"type":"server_disconnected","serverConnectionId":"first"}`)
				peer.want(t, `{"type":"server_connected","serverConnectionId":"second"}`)
			}
			first.send(t, map[string]any{"type": "broadcast", "payload": "must not arrive"})
			second.send(t, map[string]any{"type": "broadcast", "payload": "current"})
			peerB.want(t, `{"type":"message","from":"server","payload":"current"}`)
			peerA.want(t, `{"type":"message","from":"server","payload":"current"}`)
			// Omission and null are distinct; peer-to-peer routing is opaque too.
			peerA.send(t, map[string]any{"type": "send", "to": "b", "endpoint": 42})
			peerB.want(t, `{"type":"message","from":"a"}`)
			peerA.send(t, map[string]any{"type": "send", "to": "b", "payload": nil})
			peerB.want(t, `{"type":"message","from":"a","payload":null}`)
			_ = first.Close()
			_ = peerA.Close()
			second.want(t, `{"type":"peer_disconnected","peerId":"a"}`)
			_ = second.Close()
			peerB.want(t, `{"type":"server_disconnected","serverConnectionId":"second"}`)
			_ = peerB.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("empty coordinator did not retire")
			}
			for _, path := range []string{public, control} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("socket remains: %s %v", path, err)
				}
			}
		})
	}
}

func TestCoordinatorRejectsInvalidControl(t *testing.T) {
	for _, oracle := range []bool{true, false} {
		t.Run(fmt.Sprintf("upstream=%t", oracle), func(t *testing.T) {
			_, control, _ := startTestCoordinator(t, oracle)
			lease := controlDial(t, control)
			defer func() { _ = lease.Close() }()
			for _, line := range []string{"null\n", "{\n", "[]\n", "{}\n", `{"type":"send","to":"server"}` + "\n", `{"type":"register_peer","protocol":-1,"peerId":"x"}` + "\n", fmt.Sprintf(`{"type":"register_peer","protocol":%d,"peerId":"server"}`+"\n", CoordinatorProtocolVersion)} {
				conn := controlDial(t, control)
				if _, err := io.WriteString(conn, line); err != nil {
					t.Fatal(err)
				}
				conn.wantClosed(t)
			}
			peer := controlDial(t, control)
			peer.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "x"})
			peer.want(t, `{"type":"peer_registered","peerId":"x"}`)
			duplicate := controlDial(t, control)
			duplicate.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": "x"})
			duplicate.wantClosed(t)
			peer.send(t, map[string]any{"type": "broadcast", "payload": 1})
			peer.wantClosed(t)
		})
	}
}
