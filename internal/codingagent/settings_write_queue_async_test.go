package codingagent

// pi: packages/coding-agent/src/core/settings-manager.ts

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// hookedSettingsStorage is an extension SettingsStorage that calls hook inside WithLock for every write, after the manager built the text and before it is stored: a test blocks a write there, fails it, or calls the manager from it. write is the number of the write, from 1.
type hookedSettingsStorage struct {
	mu     sync.Mutex
	texts  map[string]string
	writes []string
	calls  int
	hook   func(scope string, write int) error
}

func (s *hookedSettingsStorage) WithLock(scope string, fn func(current *string) (*string, error)) error {
	s.mu.Lock()
	var current *string
	if text, ok := s.texts[scope]; ok {
		current = &text
	}
	s.mu.Unlock()
	next, err := fn(current)
	if err != nil || next == nil {
		return err
	}
	s.mu.Lock()
	s.calls++
	write, hook := s.calls, s.hook
	s.mu.Unlock()
	if hook != nil {
		if err := hook(scope, write); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.texts == nil {
		s.texts = map[string]string{}
	}
	s.texts[scope] = *next
	s.writes = append(s.writes, scope+":"+*next)
	return nil
}

// stored is the value of key in the stored text of scope, nil when there is none.
func (s *hookedSettingsStorage) stored(scope, key string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var decoded map[string]any
	_ = json.Unmarshal([]byte(s.texts[scope]), &decoded)
	return decoded[key]
}

// written lists, for every write in order, its scope and the value of key in the text it stored.
func (s *hookedSettingsStorage) written(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for _, write := range s.writes {
		scope, text, _ := strings.Cut(write, ":")
		var decoded map[string]any
		_ = json.Unmarshal([]byte(text), &decoded)
		value, _ := decoded[key].(string)
		out = append(out, scope+":"+value)
	}
	return out
}

// gateFirstWrite returns a hook that blocks the first write until the returned release runs.
func gateFirstWrite() (hook func(string, int) error, release func()) {
	gate := make(chan struct{})
	return func(_ string, write int) error {
		if write == 1 {
			<-gate
		}
		return nil
	}, sync.OnceFunc(func() { close(gate) })
}

// settings-manager.ts:576-588 chains every write on writeQueue, and each is a synchronous write in a microtask: the writes land in the order the setters ran, and a setter's write has landed before its caller resumes from its next await. Setters of several goroutines keep both: the writes are performed in the order the setters changed the settings, and each setter returns only after its own write, also when another goroutine is the one performing writes.
func TestSettingsSettersOfSeveralGoroutinesWriteInOrderAndReturnAfterTheirOwnWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hook, release := gateFirstWrite()
		storage := &hookedSettingsStorage{hook: hook}
		manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
		themes := []string{"first", "second", "third"}
		// seen[i] lists the writes stored when setter i returned: a read after the set finds its write among them.
		seen := make([][]string, len(themes))
		returned := make([]chan struct{}, len(themes))
		for i, theme := range themes {
			returned[i] = make(chan struct{})
			go func() {
				defer close(returned[i])
				if err := manager.SetTheme(theme); err != nil {
					t.Error(err)
				}
				seen[i] = storage.written("theme")
			}()
			// The setter is inside its write (the first) or waits for it (the others) before the next one starts.
			synctest.Wait()
		}
		for i := range themes {
			select {
			case <-returned[i]:
				t.Fatalf("the setter of %q returned while no write was stored (it found %v)", themes[i], seen[i])
			default:
			}
		}
		if got := manager.Get().Theme; got != "third" {
			t.Fatalf("theme in memory = %q, want third: a change is in effect at once, before its write", got)
		}
		release()
		for i := range themes {
			<-returned[i]
			if !slices.Contains(seen[i], "global:"+themes[i]) {
				t.Errorf("the setter of %q found the writes %v stored when it returned, want its own among them", themes[i], seen[i])
			}
		}
		if got, want := storage.written("theme"), []string{"global:first", "global:second", "global:third"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("writes = %v, want %v", got, want)
		}
	})
}

// settings-manager.ts:576-588: a setter called while a write runs (here from the storage's withLock) queues its write behind the running one, which Pi runs as a later microtask. The call returns without waiting for the write it is inside of, and its write follows it.
func TestSettingsSetterCalledFromInsideAWriteReturnsAndWritesAfterIt(t *testing.T) {
	storage := &hookedSettingsStorage{}
	manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
	var innerErr error
	var storedInside []string
	storage.hook = func(_ string, write int) error {
		if write == 1 {
			innerErr = manager.SetShellPath("/inner")
			// Flush and Reload cannot wait for the write they are called from either.
			_ = manager.Flush()
			storedInside = storage.written("theme")
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- manager.SetTheme("outer") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a setter called from inside a write never returned: it waits for the write it is called from")
	}
	if innerErr != nil {
		t.Fatalf("the inner setter returned %v", innerErr)
	}
	if len(storedInside) != 0 {
		t.Fatalf("writes stored when the inner setter returned = %v, want none: its write must follow the one in progress", storedInside)
	}
	if got, want := storage.written("shellPath"), []string{"global:", "global:/inner"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shellPath of the writes = %v, want %v: the outer write, then the inner setter's", got, want)
	}
	if theme, shell := storage.stored("global", "theme"), storage.stored("global", "shellPath"); theme != "outer" || shell != "/inner" {
		t.Fatalf("stored theme = %v, shellPath = %v, want outer and /inner", theme, shell)
	}
}

// A setter called from inside a write returns before its own write runs, so it cannot return that write's failure: the failure is recorded for drainErrors only, as every failure is in Pi (settings-manager.ts:585-587).
func TestSettingsSetterCalledFromInsideAWriteReturnsNilAndItsFailureIsOnlyRecorded(t *testing.T) {
	storage := &hookedSettingsStorage{}
	manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
	innerReturned := false
	var innerErr error
	storage.hook = func(_ string, write int) error {
		switch write {
		case 1:
			innerErr = manager.SetShellPath("/inner")
			innerReturned = true
		case 2:
			return errors.New("inner write failed")
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- manager.SetTheme("outer") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the outer setter returned %v, want nil: its own write succeeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a setter called from inside a write never returned")
	}
	if !innerReturned || innerErr != nil {
		t.Fatalf("the inner setter returned %v (returned: %t), want nil before its write ran", innerErr, innerReturned)
	}
	if got, want := storage.written("theme"), []string{"global:outer"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %v, want %v", got, want)
	}
	drained := manager.DrainErrors()
	if len(drained) != 1 || drained[0].Scope != "global" || drained[0].Error.Error() != "inner write failed" {
		t.Fatalf("drained = %+v, want the failure of the inner setter's write", drained)
	}
	if got := manager.Get().ShellPath; got != "/inner" {
		t.Fatalf("shellPath in memory = %q, want /inner", got)
	}
}

// settings-manager.ts:576-588: a write that throws is caught by the queue and recorded (:585-587), the change of its setter stays in effect, the writes after it still run, and drainErrors (:674-678) hands the error out once. PiG's setter, which returns after its write, also returns the failure.
func TestSettingsWriteFailureIsRecordedOnceAndTheQueueGoesOn(t *testing.T) {
	failure := errors.New("disk full")
	storage := &hookedSettingsStorage{hook: func(_ string, write int) error {
		switch write {
		case 1:
			return failure
		case 2:
			panic("storage broke")
		}
		return nil
	}}
	manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
	for i, theme := range []string{"lost", "lost too", "kept"} {
		err := manager.SetTheme(theme)
		if want := []string{"disk full", "storage broke", ""}[i]; (err == nil) != (want == "") || err != nil && err.Error() != want {
			t.Fatalf("SetTheme(%q) returned %v, want its own write's failure %q", theme, err, want)
		}
		if got := manager.Get().Theme; got != theme {
			t.Fatalf("theme in memory = %q, want %q: the change stays in effect when its write fails", got, theme)
		}
	}
	if got, want := storage.written("theme"), []string{"global:kept"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %v, want %v: the write after the failed ones ran", got, want)
	}
	drained := manager.DrainErrors()
	var got []string
	for _, e := range drained {
		got = append(got, e.Scope+": "+e.Error.Error())
	}
	if want := []string{"global: disk full", "global: storage broke"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("drained = %v, want %v", got, want)
	}
	if !errors.Is(drained[0].Error, failure) {
		t.Fatalf("recorded error = %v, want the storage's own error", drained[0].Error)
	}
	if again := manager.DrainErrors(); len(again) != 0 {
		t.Fatalf("second drain = %+v, want none", again)
	}
}

// settings-manager.ts:578-584: the fields a setter marks modified are cleared only when a write of their scope succeeds, so the write after a failed one also saves what the failed one did not.
func TestSettingsWriteAfterAFailedOneSavesWhatItDidNotSave(t *testing.T) {
	storage := &hookedSettingsStorage{hook: func(_ string, write int) error {
		if write == 1 {
			return errors.New("disk full")
		}
		return nil
	}}
	manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
	if err := manager.SetTheme("dark"); err == nil {
		t.Fatal("the first write did not fail")
	}
	if err := manager.SetShellPath("/bin/zsh"); err != nil {
		t.Fatal(err)
	}
	if theme, shell := storage.stored("global", "theme"), storage.stored("global", "shellPath"); theme != "dark" || shell != "/bin/zsh" {
		t.Fatalf("stored theme = %v, shellPath = %v, want dark and /bin/zsh: the second write carries the change the failed write did not save", theme, shell)
	}
	// The successful write cleared the marks: a third write saves only its own change over what the storage holds now.
	storage.mu.Lock()
	storage.texts["global"] = `{"shellPath":"/bin/zsh"}`
	storage.mu.Unlock()
	if err := manager.SetDefaultModel("m"); err != nil {
		t.Fatal(err)
	}
	if theme, model := storage.stored("global", "theme"), storage.stored("global", "defaultModel"); theme != nil || model != "m" {
		t.Fatalf("stored theme = %v, defaultModel = %v, want no theme and m: a saved change is not written again", theme, model)
	}
}

// settings-manager.ts:578-581: the queue repeats assertProjectTrustedForWrite when it runs a project write, so a project that lost its trust after the setter is not written and the refusal is recorded. Here the project write waits behind a write of another goroutine while the trust is withdrawn.
func TestQueuedProjectWriteIsRefusedWhenTheTrustWasWithdrawnBeforeItRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hook, release := gateFirstWrite()
		storage := &hookedSettingsStorage{hook: hook}
		manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
		global, project := make(chan error, 1), make(chan error, 1)
		go func() { global <- manager.SetTheme("blocker") }()
		synctest.Wait()
		go func() { project <- manager.SetProjectExtensionPaths([]string{"ext"}) }()
		synctest.Wait()
		manager.SetProjectTrusted(false)
		release()
		if err := <-global; err != nil {
			t.Fatal(err)
		}
		if err := <-project; err == nil || err.Error() != "Project is not trusted; refusing to write project settings" {
			t.Fatalf("the project setter returned %v, want the refusal of its write", err)
		}
		if got, want := storage.written("theme"), []string{"global:blocker"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("writes = %v, want %v: the project is no longer trusted when its write runs", got, want)
		}
		drained := manager.DrainErrors()
		if len(drained) != 1 || drained[0].Scope != "project" || drained[0].Error.Error() != "Project is not trusted; refusing to write project settings" {
			t.Fatalf("drained = %+v, want the refusal of the project write", drained)
		}
		// A setter called after the trust was withdrawn is refused at once (:645, :662).
		if err := manager.SetProjectExtensionPaths([]string{"later"}); err == nil || err.Error() != "Project is not trusted; refusing to write project settings" {
			t.Fatalf("a project setter of an untrusted project returned %v", err)
		}
	})
}

// settings-manager.ts:500-501 and :670-672: reload() and flush() await writeQueue, so both return only after the writes queued before them, and reload reads the storage after them.
func TestSettingsFlushAndReloadWaitForAWriteInProgress(t *testing.T) {
	for _, wait := range []struct {
		name string
		run  func(*SettingsManager)
	}{
		{"Flush", func(manager *SettingsManager) { _ = manager.Flush() }},
		{"Reload", func(manager *SettingsManager) { manager.Reload() }},
	} {
		t.Run(wait.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				hook, release := gateFirstWrite()
				storage := &hookedSettingsStorage{hook: hook}
				manager := NewSettingsManagerFromStorage(storage, SettingsManagerCreateOptions{})
				set := make(chan error, 1)
				go func() { set <- manager.SetTheme("queued") }()
				synctest.Wait()
				// The stored theme when the wait returned.
				var seen any
				waited := make(chan struct{})
				go func() {
					defer close(waited)
					wait.run(manager)
					seen = storage.stored("global", "theme")
				}()
				synctest.Wait()
				select {
				case <-waited:
					t.Fatalf("%s returned while a write was in progress (stored theme %v)", wait.name, seen)
				default:
				}
				release()
				<-waited
				if err := <-set; err != nil {
					t.Fatal(err)
				}
				if seen != "queued" {
					t.Fatalf("stored theme when %s returned = %v, want queued", wait.name, seen)
				}
				if got := manager.Get().Theme; got != "queued" {
					t.Fatalf("theme after %s = %q, want queued", wait.name, got)
				}
			})
		})
	}
}
