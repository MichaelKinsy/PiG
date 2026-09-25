package tui

import "testing"

func envOf(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// Ports upstream terminal.test.ts "resolveEscapeTimeoutMs".
func TestResolveEscapeTimeoutMs(t *testing.T) {
	t.Run("uses PI_TUI_ESC_TIMEOUT when configured", func(t *testing.T) {
		if got := ResolveEscapeTimeoutMs(envOf(map[string]string{"PI_TUI_ESC_TIMEOUT": "80"})); got != 80 {
			t.Fatalf("got %v, want 80", got)
		}
		if got := ResolveEscapeTimeoutMs(envOf(map[string]string{"PI_TUI_ESC_TIMEOUT": "80", "SSH_TTY": "/dev/pts/1"})); got != 80 {
			t.Fatalf("got %v, want 80", got)
		}
	})
	t.Run("ignores invalid PI_TUI_ESC_TIMEOUT values", func(t *testing.T) {
		for _, value := range []string{"abc", "0", "-5", "", "Infinity", "NaN", "80ms"} {
			if got := ResolveEscapeTimeoutMs(envOf(map[string]string{"PI_TUI_ESC_TIMEOUT": value})); got != 10 {
				t.Fatalf("PI_TUI_ESC_TIMEOUT=%q: got %v, want 10", value, got)
			}
		}
	})
	t.Run("accepts JavaScript Number spellings", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  float64
		}{{"\t25 ", 25}, {"1e2", 100}, {"0x10", 16}, {"12.5", 12.5}} {
			if got := ResolveEscapeTimeoutMs(envOf(map[string]string{"PI_TUI_ESC_TIMEOUT": tc.value})); got != tc.want {
				t.Fatalf("PI_TUI_ESC_TIMEOUT=%q: got %v, want %v", tc.value, got, tc.want)
			}
		}
	})
	t.Run("defaults to 100ms over SSH", func(t *testing.T) {
		if got := ResolveEscapeTimeoutMs(envOf(map[string]string{"SSH_CONNECTION": "10.0.0.1 22"})); got != 100 {
			t.Fatalf("got %v, want 100", got)
		}
		if got := ResolveEscapeTimeoutMs(envOf(map[string]string{"SSH_TTY": "/dev/pts/1"})); got != 100 {
			t.Fatalf("got %v, want 100", got)
		}
	})
	t.Run("defaults to 10ms otherwise", func(t *testing.T) {
		if got := ResolveEscapeTimeoutMs(envOf(nil)); got != 10 {
			t.Fatalf("got %v, want 10", got)
		}
	})
}
