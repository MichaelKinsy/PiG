package env

// Ports packages/env/test/connection.test.ts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// shellQuote is text as one single-quoted shell word.
func shellQuote(text string) string { return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'" }

// failing is a script that writes stderr and exits with code.
func failing(stderr string, code int) string {
	return fmt.Sprintf("printf '%%s' %s >&2; exit %d", shellQuote(stderr), code)
}

func startError(t *testing.T, connection *Connection) error {
	t.Helper()
	_, err := connection.Info(background)
	if err == nil {
		t.Fatal("the start did not fail")
	}
	return err
}

func startMessage(t *testing.T, script string, onLog func(string)) string {
	t.Helper()
	err := startError(t, scriptedConnection(t, script, onLog))
	if !IsConnectionLost(err) {
		t.Fatalf("start error %v is not a lost connection", err)
	}
	return err.Error()
}

func TestConnectionStartFailures(t *testing.T) {
	t.Parallel()

	// connection.test.ts "includes the end of stderr when the daemon exits before it is ready"
	t.Run("includes the end of stderr when the daemon exits before it is ready", func(t *testing.T) {
		t.Parallel()
		got := startMessage(t, failing("ssh: Permission denied (publickey).\n", 255), nil)
		want := "pi-env exited with code 255 before it was ready\nremote stderr:\nssh: Permission denied (publickey)."
		if got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
	})

	// connection.test.ts "keeps the message unchanged when the daemon wrote nothing printable to stderr"
	t.Run("keeps the message unchanged when the daemon wrote nothing printable to stderr", func(t *testing.T) {
		t.Parallel()
		want := "pi-env exited with code 3 before it was ready"
		if got := startMessage(t, "exit 3", nil); got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
		if got := startMessage(t, failing(" \n\u0007\u001b[0m\n", 3), nil); got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
	})

	// connection.test.ts "drops terminal escapes and C0 and C1 control characters, keeping other text"
	t.Run("drops terminal escapes and C0 and C1 control characters, keeping other text", func(t *testing.T) {
		t.Parallel()
		stderr := "\u001b[31mred\u001b[0m\r\nbell\u0007 del\u007f\tc1:\u0085\u009d end\nÜberprüfung fehlgeschlagen ✗\n\n"
		want := "pi-env exited with code 1 before it was ready\nremote stderr:\nred\nbell del\tc1: end\nÜberprüfung fehlgeschlagen ✗"
		if got := startMessage(t, failing(stderr, 1), nil); got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
	})

	// connection.test.ts "keeps only the last 20 lines of stderr"
	t.Run("keeps only the last 20 lines of stderr", func(t *testing.T) {
		t.Parallel()
		lines := make([]string, 30)
		for index := range lines {
			lines[index] = fmt.Sprintf("line %d", index)
		}
		want := "pi-env exited with code 1 before it was ready\nremote stderr:\n" + strings.Join(lines[10:], "\n")
		if got := startMessage(t, failing(strings.Join(lines, "\n")+"\n", 1), nil); got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
	})

	// connection.test.ts "keeps only the last 2000 characters of stderr"
	t.Run("keeps only the last 2000 characters of stderr", func(t *testing.T) {
		t.Parallel()
		got := startMessage(t, failing("first\n"+strings.Repeat("x", 5000)+"\n", 1), nil)
		_, tail, _ := strings.Cut(got, "\nremote stderr:\n")
		if want := strings.Repeat("x", 1999); tail != want {
			t.Fatalf("tail has %d characters, want %d", len(tail), len(want))
		}
	})

	// connection.test.ts "passes stderr to onLog unchanged"
	t.Run("passes stderr to onLog unchanged", func(t *testing.T) {
		t.Parallel()
		var mu sync.Mutex
		var logged strings.Builder
		startMessage(t, failing("\u001b[31mred\u001b[0m\n", 1), func(text string) {
			mu.Lock()
			defer mu.Unlock()
			logged.WriteString(text)
		})
		mu.Lock()
		defer mu.Unlock()
		if want := "\u001b[31mred\u001b[0m\n"; logged.String() != want {
			t.Fatalf("onLog received %q, want %q", logged.String(), want)
		}
	})

	// connection.test.ts "decodes %s split across stderr chunks" (#10716): the first read is acknowledged before the
	// remaining UTF-8 bytes are written.
	for _, character := range []string{"\u00e9", "\u9519", "\U0001f600"} {
		t.Run("decodes "+character+" split across stderr chunks", func(t *testing.T) {
			t.Parallel()
			acknowledged := filepath.Join(t.TempDir(), "logged")
			stderr := "prefix " + character + " suffix\n"
			split := len("prefix ") + 1
			bytes := []byte(stderr)
			script := fmt.Sprintf(
				"printf '%s' >&2; n=0; while [ ! -e %s ] && [ $n -lt 600 ]; do sleep 0.005; n=$((n+1)); done; "+
					"printf '%s' >&2; exit 7",
				octal(bytes[:split]), shellQuote(acknowledged), octal(bytes[split:]))
			var mu sync.Mutex
			var logged []string
			got := startMessage(t, script, func(text string) {
				mu.Lock()
				defer mu.Unlock()
				logged = append(logged, text)
				if _, err := os.Stat(acknowledged); err != nil {
					_ = os.WriteFile(acknowledged, nil, 0o600)
				}
			})
			mu.Lock()
			defer mu.Unlock()
			if len(logged) < 2 {
				t.Fatalf("onLog received %d chunks, want at least 2", len(logged))
			}
			if joined := strings.Join(logged, ""); joined != stderr {
				t.Fatalf("onLog received %q, want %q", joined, stderr)
			}
			want := "pi-env exited with code 7 before it was ready\nremote stderr:\n" + strings.TrimRight(stderr, " \n")
			if got != want {
				t.Fatalf("message = %q, want %q", got, want)
			}
		})
	}

	// connection.test.ts "flushes an incomplete UTF-8 character when stderr ends"
	t.Run("flushes an incomplete UTF-8 character when stderr ends", func(t *testing.T) {
		t.Parallel()
		var mu sync.Mutex
		var logged strings.Builder
		got := startMessage(t, `printf '\170\360\237' >&2; exit 7`, func(text string) {
			mu.Lock()
			defer mu.Unlock()
			logged.WriteString(text)
		})
		mu.Lock()
		defer mu.Unlock()
		if logged.String() != "x\ufffd" {
			t.Fatalf("onLog received %q, want %q", logged.String(), "x\ufffd")
		}
		if want := "pi-env exited with code 7 before it was ready\nremote stderr:\nx\ufffd"; got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
	})

	// connection.test.ts "reports only the stderr of the attempt that failed"
	t.Run("reports only the stderr of the attempt that failed", func(t *testing.T) {
		t.Parallel()
		attempt := 0
		connection := NewConnection(ConnectionOptions{CommandFunc: func(context.Context) ([]string, error) {
			attempt++
			return []string{"sh", "-c", failing(fmt.Sprintf("failure %d\n", attempt), 1), "sh"}, nil
		}})
		t.Cleanup(connection.Close)
		if first := startError(t, connection).Error(); !strings.Contains(first, "failure 1") {
			t.Fatalf("first message = %q", first)
		}
		second := startError(t, connection).Error()
		if !strings.Contains(second, "failure 2") || strings.Contains(second, "failure 1") {
			t.Fatalf("second message = %q", second)
		}
	})

	// connection.test.ts "keeps the fields of a hello the daemon refused"
	t.Run("keeps the fields of a hello the daemon refused", func(t *testing.T) {
		t.Parallel()
		// The two attempts' hellos are requests 1 and 2 of the connection; a reply to an id nobody waits for is ignored.
		refusal := Json{"code": "EINVAL", "message": "hello refused", "path": "/daemon", "extra": 1}
		replies := append(mustFrame(frameError, 1, refusal, nil), mustFrame(frameError, 2, refusal, nil)...)
		connection := scriptedConnection(t, syncLine+"printf '"+octal(replies)+"'; exec sleep 30", nil)
		for attempt := range 2 {
			remote, ok := startError(t, connection).(*RemoteError)
			if !ok {
				t.Fatalf("attempt %d: start error is not a RemoteError", attempt)
			}
			if remote.Code != "EINVAL" || remote.Path == nil || *remote.Path != "/daemon" || remote.Fields["extra"] != float64(1) {
				t.Fatalf("attempt %d: fields = %+v", attempt, remote)
			}
			if !strings.HasPrefix(remote.Message, "hello refused") || IsConnectionLost(remote) {
				t.Fatalf("attempt %d: message %q, lost %v", attempt, remote.Message, IsConnectionLost(remote))
			}
		}
	})

	// connection.test.ts "reports a daemon that cannot be started as a spawn error"
	t.Run("reports a daemon that cannot be started as a spawn error", func(t *testing.T) {
		t.Parallel()
		connection := NewConnection(ConnectionOptions{Command: []string{"/nonexistent/pi-env"}})
		t.Cleanup(connection.Close)
		remote, ok := startError(t, connection).(*RemoteError)
		if !ok || remote.Code != "spawn_error" || !IsConnectionLost(remote) {
			t.Fatalf("start error = %#v, want a lost spawn_error", remote)
		}
	})
}

// connection.test.ts "fails pending requests as soon as the daemon exits, even while its pipes stay open"
func TestConnectionOfAReadyDaemonFailsPendingRequestsWhenItExitsWhilePipesStayOpen(t *testing.T) {
	t.Parallel()
	// The first request byte after the hello makes the daemon exit while a process it started holds stdout and stderr.
	hello, err := encodeRequest(1, "hello", Json{"protocol": 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	connection := scriptedConnection(t,
		syncLine+helloResult+fmt.Sprintf(`head -c %d >/dev/null; sleep 3 & exit 0`, len(hello)+1), nil)
	if _, err := connection.Info(background); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = connection.Request(background, "lstat", Json{"path": "/"}, RequestOptions{})
	if err == nil || !IsConnectionLost(err) || err.Error() != "pi-env connection lost" {
		t.Fatalf("request error = %v, want pi-env connection lost", err)
	}
	// The process the daemon started holds its pipes for 3 s; the request must not wait for them.
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("the request took %v", elapsed)
	}
}
