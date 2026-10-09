package env

import (
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Scripted daemons: `$3` is the token Connection appends as `serve --token <token>`. The hello is request 1.
const (
	syncLine = `printf 'PI-ENV %s\n' "$3"; `
	// A result frame for request 1 whose JSON is {"protocol":1}: length 23 = 9 + 14.
	helloResult = `printf '\000\000\000\027\002\000\000\000\001\000\000\000\016{"protocol":1}'; `
)

func scriptedConnection(t *testing.T, script string, onLog func(string)) *Connection {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	connection := NewConnection(ConnectionOptions{Command: []string{"sh", "-c", script, "sh"}, OnLog: onLog})
	t.Cleanup(connection.Close)
	return connection
}

// packages/env/src/connection.ts:339 #onData: output before the sync line is shell startup noise; it is trimmed and reported through onLog
// once (String.prototype.trim), and nothing is reported when it is only whitespace.
func TestConnectionReportsShellNoiseBeforeTheSyncLineThroughOnLog(t *testing.T) {
	for _, tc := range []struct {
		name, noise string
		want        []string
	}{
		{"noise", `printf '\n  motd banner\nlast login  \n'; `, []string{"motd banner\nlast login"}},
		{"whitespace only", `printf ' \n\t\n'; `, nil},
		{"none", ``, nil},
		// String.prototype.trim removes U+FEFF and keeps U+0085, unlike Go's unicode.IsSpace.
		{"JavaScript whitespace", `printf '\357\273\277banner\302\205'; `, []string{"banner\u0085"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var logs []string
			connection := scriptedConnection(t, tc.noise+syncLine+helloResult+`exec sleep 30`, func(text string) {
				mu.Lock()
				defer mu.Unlock()
				logs = append(logs, text)
			})
			if _, err := connection.Info(background); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(logs, tc.want) {
				t.Fatalf("onLog calls %q, want %q", logs, tc.want)
			}
		})
	}
}

// connection.ts #onData: a frame length below 9 or above MAX_FRAME (16 MiB) tears the session down with
// `Corrupt frame from pi-env`, which rejects the pending hello before the killed daemon's exit can, so the start fails
// with that error and not with the exit.
// Pi source: packages/env/src/connection.ts
// mutation-checked: zeroing the results of Connection.Info fails it
// packages/env/src/connection.ts:61,284,339: ConnectionOptions.onLog receives the daemon stderr and noise text.
func TestConnectionRejectsAFrameLengthOutsideItsBounds(t *testing.T) {
	for _, tc := range []struct{ name, length string }{
		{"below 9", `\000\000\000\010`},
		{"above 16 MiB", `\001\000\000\001`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range 20 {
				connection := scriptedConnection(t, syncLine+`printf '`+tc.length+`'; exec sleep 30`, nil)
				_, err := connection.Info(background)
				if err == nil || err.Error() != "Corrupt frame from pi-env" || !IsConnectionLost(err) {
					t.Fatalf("Info() error = %v, want the lost error `Corrupt frame from pi-env`", err)
				}
				connection.Close()
			}
		})
	}
}

// connection.ts #onData: past 1 MiB of output without the sync line, only the last marker-length bytes are kept, so a
// sync line split across that cut is still found, and the noise reported is only what was kept.
// mutation-checked: dropping the reads and writes of ConnectionOptions.OnLog fails it
// Pi: packages/env/src/connection.ts:61 (onLog)
// packages/env/src/connection.ts:61,284: ConnectionOptions.onLog receives the daemon stderr text.
func TestConnectionFindsASyncLineSplitAcrossTheNoiseCut(t *testing.T) {
	var logs []string
	connection := NewConnection(ConnectionOptions{OnLog: func(text string) { logs = append(logs, text) }})
	s := &session{token: "0123456789abcdef0123456789abcdef"}
	marker := "PI-ENV " + s.token + "\n"
	noise := strings.Repeat("x", 1024*1024+100)
	chunks := &chunkReader{chunks: []string{noise + marker[:10], marker[10:] + "rest"}}
	rest, ok := connection.syncStream(s, chunks)
	if !ok {
		t.Fatal("the sync line split across the noise cut was not found")
	}
	if got := string(must(io.ReadAll(rest))); got != "rest" {
		t.Fatalf("after the sync line %q, want %q", got, "rest")
	}
	if want := []string{strings.Repeat("x", len(marker)-10)}; !slices.Equal(logs, want) {
		t.Fatalf("onLog calls %q, want %q", logs, want)
	}
}

// chunkReader returns one chunk per Read, then io.EOF.
type chunkReader struct{ chunks []string }

func (r *chunkReader) Read(buffer []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	read := copy(buffer, r.chunks[0])
	r.chunks[0] = r.chunks[0][read:]
	if r.chunks[0] == "" {
		r.chunks = r.chunks[1:]
	}
	return read, nil
}
