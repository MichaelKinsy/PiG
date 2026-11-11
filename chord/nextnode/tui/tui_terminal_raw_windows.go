//go:build windows

package tui

import "golang.org/x/sys/windows"

// rawState is the console mode saved before raw mode.
type rawState uint32

// makeRaw clears echo, line input and processed input on the console input fd and turns on virtual terminal input: the settings of
// x/term v0.47.0 MakeRaw on Windows, which does not touch ENABLE_PROCESSED_OUTPUT.
func makeRaw(fd int) (*rawState, error) {
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(fd), &mode); err != nil {
		return nil, err
	}
	raw := mode &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT)
	raw |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	if err := windows.SetConsoleMode(windows.Handle(fd), raw); err != nil {
		return nil, err
	}
	saved := rawState(mode)
	return &saved, nil
}

func restoreRaw(fd int, state *rawState) error {
	return windows.SetConsoleMode(windows.Handle(fd), uint32(*state))
}
