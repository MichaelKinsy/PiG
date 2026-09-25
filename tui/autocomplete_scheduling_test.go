package tui

import "testing"

func TestLocalAutocompleteWaitsForOwnerAndRejectsCancelledResult(t *testing.T) {
	e := NewEditor()
	e.SetAutocomplete(NewSlashOnlyProvider(sampleCommands()))
	var tasks []func()
	e.SetAsyncApply(func(apply func()) { tasks = append(tasks, apply) })
	e.HandleInput("/hel")
	if e.AutocompleteOpen() {
		t.Fatal("local results appeared before asynchronous owner application")
	}
	if len(tasks) != 1 {
		t.Fatalf("queued results = %d", len(tasks))
	}
	tasks[0]()
	if !e.AutocompleteOpen() {
		t.Fatal("owner did not apply local results")
	}
	e.Clear()
	tasks = nil
	e.HandleInput("/hel")
	e.AutocompleteCancel()
	tasks[0]()
	if e.AutocompleteOpen() {
		t.Fatal("cancelled request reopened popup")
	}
	tasks = nil
	e.HandleInput("p")
	e.Clear()
	tasks[0]()
	if e.AutocompleteOpen() {
		t.Fatal("submitted text's results reopened popup")
	}
}
