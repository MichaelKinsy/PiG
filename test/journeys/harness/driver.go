package harness

// Driver is the process a Run drives and observes: a real terminal for the interactive journeys, a pair of pipes for the RPC journeys. Screen is what the process has shown, so every wait predicate and every compared frame reads it.
type Driver interface {
	// Changed returns a channel closed by the next change of what Screen reports.
	Changed() <-chan struct{}
	// Outputs counts the output events so far.
	Outputs() int
	Screen() (string, error)
	// History is everything the process has shown, including what scrolled away.
	History() (string, error)
	Exited() <-chan struct{}
	ExitCode() (int, error)
	PID() int
	Stderr() string
	Close()
	Resize(w, h int) error
	SendText(s string) error
	SendKeys(keys ...string) error
	SendBytes(b []byte) error
	// Quit asks the process to leave the way its mode ends a session: Ctrl+D at an empty prompt, end of standard input for RPC.
	Quit() error
}

var _ Driver = (*Terminal)(nil)

// Quit leaves the interactive prompt with Ctrl+D.
func (t *Terminal) Quit() error { return t.SendKeys("C-d") }
