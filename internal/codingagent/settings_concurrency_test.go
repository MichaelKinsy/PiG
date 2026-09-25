package codingagent

import "testing"

// Background readers (a cache-warming refresh reads its mode on a timer
// goroutine) run while /settings writes; the settings layers must tolerate
// that under the race detector.
func TestSettingsManagerConcurrentReadAndWrite(t *testing.T) {
	sm := NewSettingsManager(t.TempDir(), t.TempDir())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			_ = sm.GetGlobalSettings()
			_ = sm.Get()
		}
	}()
	for _, theme := range []string{"dark", "light", "dark"} {
		if err := sm.UpdateGlobal(func(s *Settings) { s.Theme = theme }); err != nil {
			t.Fatal(err)
		}
	}
	<-done
	if got := sm.Get().Theme; got != "dark" {
		t.Fatalf("theme = %q, want dark", got)
	}
}
