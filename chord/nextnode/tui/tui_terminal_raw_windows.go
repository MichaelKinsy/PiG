//go:build windows

package tui

import "golang.org/x/sys/windows"

// rawState is the console mode saved before raw mode.
type rawState uint32

// makeRaw clears echo, line input and processed input and output on the console input fd (the settings of x/term.MakeRaw on Windows).
func makeRaw(fd int) (*rawState, error) {
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(fd), &mode); err != nil {
		return nil, err
	}
	raw := mode &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_OUTPUT)
	if err := windows.SetConsoleMode(windows.Handle(fd), raw); err != nil {
		return nil, err
	}
	saved := rawState(mode)
	return &saved, nil
}

func restoreRaw(fd int, state *rawState) error {
	return windows.SetConsoleMode(windows.Handle(fd), uint32(*state))
}
