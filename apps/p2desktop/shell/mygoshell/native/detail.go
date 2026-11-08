package native

// selectAfter selects a session the server created, from any goroutine.
func (a *App) selectAfter(id string) {
	a.setSel(id)
	a.Invalidate()
}

func (a *App) sel() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.selected
}

func (a *App) setSel(id string) {
	a.mu.Lock()
	a.selected = id
	a.mu.Unlock()
}

// OpenSession selects a session (a deep link, a notification): the list shows it chosen and its sheet opens.
func (a *App) OpenSession(id string) {
	a.mu.Lock()
	a.selected, a.creating, a.archived = id, false, false
	a.mu.Unlock()
	a.Invalidate()
}
