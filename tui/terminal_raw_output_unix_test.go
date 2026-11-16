//go:build linux || darwin

package tui

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"golang.org/x/sys/unix"
)

// libuv's raw mode (uv_tty_set_mode, deps/uv/src/unix/tty.c: `tmp.c_oflag |= (ONLCR)`) leaves the output processing the terminal had and adds ONLCR, so
// the kernel turns every LF Node writes into CR LF, and Pi's `\r\n` reaches a terminal as `\r\r\n`. x/term's MakeRaw clears OPOST, so PiG's `\r\n` reached it as
// written, and a bare LF of a child process that shares the terminal moved down without returning to column 0. The screen of Pi's own writes is the same;
// a transcript of the bytes is not, at every line break a program writes.
func TestRawModeKeepsTheKernelsNewlineTranslationAsLibuvDoes(t *testing.T) {
	master, slave := openTestPTY(t)
	fd := int(slave.Fd())
	cooked, err := unix.IoctlGetTermios(fd, ttyGetAttributes)
	if err != nil {
		t.Fatal(err)
	}
	if cooked.Oflag&unix.OPOST == 0 || cooked.Oflag&unix.ONLCR == 0 {
		t.Skipf("the terminal starts without output processing (oflag %#x): there is nothing to keep", cooked.Oflag)
	}
	state, err := MakeRaw(fd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = RestoreCooked(fd, state) })

	const written = "a\r\nb\n"
	if _, err := slave.WriteString(written); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := master.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(buf[:n]), "a\r\r\nb\r\n"; got != want {
		t.Errorf("the terminal received %q from %q, want %q: the kernel translates each LF as it does for a program under libuv's raw mode", got, written, want)
	}
}

// termiosFlag is one flag of the table test/parity/testdata/raw-termios-probe.py reads, in the probe's order.
type termiosFlag struct {
	word, name string
	bit        uint64
}

var termiosFlags = []termiosFlag{
	{"iflag", "IGNBRK", unix.IGNBRK}, {"iflag", "BRKINT", unix.BRKINT}, {"iflag", "IGNPAR", unix.IGNPAR}, {"iflag", "PARMRK", unix.PARMRK},
	{"iflag", "INPCK", unix.INPCK}, {"iflag", "ISTRIP", unix.ISTRIP}, {"iflag", "INLCR", unix.INLCR}, {"iflag", "IGNCR", unix.IGNCR},
	{"iflag", "ICRNL", unix.ICRNL}, {"iflag", "IXON", unix.IXON}, {"iflag", "IXOFF", unix.IXOFF}, {"iflag", "IXANY", unix.IXANY}, {"iflag", "IMAXBEL", unix.IMAXBEL},
	{"oflag", "OPOST", unix.OPOST}, {"oflag", "ONLCR", unix.ONLCR}, {"oflag", "OCRNL", unix.OCRNL}, {"oflag", "ONOCR", unix.ONOCR}, {"oflag", "ONLRET", unix.ONLRET},
	{"cflag", "PARENB", unix.PARENB}, {"cflag", "PARODD", unix.PARODD}, {"cflag", "CSTOPB", unix.CSTOPB}, {"cflag", "CREAD", unix.CREAD},
	{"cflag", "HUPCL", unix.HUPCL}, {"cflag", "CLOCAL", unix.CLOCAL},
	{"lflag", "ISIG", unix.ISIG}, {"lflag", "ICANON", unix.ICANON}, {"lflag", "ECHO", unix.ECHO}, {"lflag", "ECHOE", unix.ECHOE}, {"lflag", "ECHOK", unix.ECHOK},
	{"lflag", "ECHONL", unix.ECHONL}, {"lflag", "NOFLSH", unix.NOFLSH}, {"lflag", "TOSTOP", unix.TOSTOP}, {"lflag", "IEXTEN", unix.IEXTEN},
	{"lflag", "ECHOCTL", unix.ECHOCTL}, {"lflag", "ECHOKE", unix.ECHOKE},
}

func termiosWord(termios *unix.Termios, word string) uint64 {
	switch word {
	case "iflag":
		return uint64(termios.Iflag)
	case "oflag":
		return uint64(termios.Oflag)
	case "cflag":
		return uint64(termios.Cflag)
	}
	return uint64(termios.Lflag)
}

func withTermiosFlag[T ~uint32 | ~uint64](word T, bit uint64, set bool) T {
	if set {
		return word | T(bit)
	}
	return word &^ T(bit)
}

// termiosVariant is the cooked mode the probe asks the pseudo-terminal for before it starts the program: every flag of the table set or cleared, and the
// control characters VMIN and VTIME different from what raw mode sets. The terminal's own answer (the kernel keeps some flags of a pseudo-terminal fixed) is
// the cooked mode the program starts in.
func termiosVariant(termios unix.Termios, ones bool) *unix.Termios {
	for _, flag := range termiosFlags {
		switch flag.word {
		case "iflag":
			termios.Iflag = withTermiosFlag(termios.Iflag, flag.bit, ones)
		case "oflag":
			termios.Oflag = withTermiosFlag(termios.Oflag, flag.bit, ones)
		case "cflag":
			termios.Cflag = withTermiosFlag(termios.Cflag, flag.bit, ones)
		case "lflag":
			termios.Lflag = withTermiosFlag(termios.Lflag, flag.bit, ones)
		}
	}
	termios.Cc[unix.VMIN] = 3
	termios.Cc[unix.VTIME] = 7
	return &termios
}

// termiosDelta lists the flags that differ between two modes, as the probe does: +NAME set, -NAME cleared.
func termiosDelta(before, after *unix.Termios) []string {
	flags := []string{}
	for _, flag := range termiosFlags {
		was, is := termiosWord(before, flag.word)&flag.bit != 0, termiosWord(after, flag.word)&flag.bit != 0
		switch {
		case was && !is:
			flags = append(flags, "-"+flag.name)
		case !was && is:
			flags = append(flags, "+"+flag.name)
		}
	}
	return flags
}

type recordedRawMode struct {
	Running        []string `json:"running"`
	VMin           int      `json:"vmin"`
	VTime          int      `json:"vtime"`
	AfterExit      []string `json:"after_exit"`
	AfterExitVMin  bool     `json:"after_exit_vmin"`
	AfterExitVTime bool     `json:"after_exit_vtime"`
}

// The fixture is what Pi's own process did, recorded by the probe on a pseudo-terminal whose cooked mode had every flag of the table set, and cleared:
//
//	python3 test/parity/testdata/raw-termios-probe.py pi > tui/testdata/pi-raw-termios.json
//
// MakeRaw puts the same pseudo-terminal in the same raw mode: every flag libuv's UV_TTY_MODE_RAW changes, and none it leaves alone, and the restore hands
// the cooked mode back whole. (The probe runs the whole program, so the parity scenario startup/25-raw-mode-termios compares what pig's TUI adds as well.)
func TestRawModeIsTheModePiRecorded(t *testing.T) {
	data, err := os.ReadFile("testdata/pi-raw-termios.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded map[string]recordedRawMode
	if err := json.Unmarshal(data, &recorded); err != nil {
		t.Fatal(err)
	}
	for name, ones := range map[string]bool{"cooked_all_flags_set": true, "cooked_all_flags_cleared": false} {
		want, ok := recorded[name]
		if !ok {
			t.Fatalf("the fixture has no record %q", name)
		}
		t.Run(name, func(t *testing.T) {
			_, slave := openTestPTY(t)
			fd := int(slave.Fd())
			initial, err := unix.IoctlGetTermios(fd, ttyGetAttributes)
			if err != nil {
				t.Fatal(err)
			}
			if err := unix.IoctlSetTermios(fd, ioctlWriteTermiosDrain, termiosVariant(*initial, ones)); err != nil {
				t.Fatal(err)
			}
			cooked, err := unix.IoctlGetTermios(fd, ttyGetAttributes)
			if err != nil {
				t.Fatal(err)
			}
			state, err := MakeRaw(fd)
			if err != nil {
				t.Fatal(err)
			}
			running, err := unix.IoctlGetTermios(fd, ttyGetAttributes)
			if err != nil {
				t.Fatal(err)
			}
			if got := termiosDelta(cooked, running); !slices.Equal(got, want.Running) {
				t.Errorf("raw mode changed the flags %v, Pi's changed %v", got, want.Running)
			}
			if got := [2]int{int(running.Cc[unix.VMIN]), int(running.Cc[unix.VTIME])}; got != [2]int{want.VMin, want.VTime} {
				t.Errorf("raw mode has VMIN, VTIME = %v, Pi's has %v", got, [2]int{want.VMin, want.VTime})
			}
			if err := RestoreCooked(fd, state); err != nil {
				t.Fatal(err)
			}
			after, err := unix.IoctlGetTermios(fd, ttyGetAttributes)
			if err != nil {
				t.Fatal(err)
			}
			if got := termiosDelta(cooked, after); !slices.Equal(got, want.AfterExit) {
				t.Errorf("the restored mode differs from the cooked one in %v, Pi's in %v", got, want.AfterExit)
			}
			if got := after.Cc[unix.VMIN] == cooked.Cc[unix.VMIN] && after.Cc[unix.VTIME] == cooked.Cc[unix.VTIME]; got != (want.AfterExitVMin && want.AfterExitVTime) {
				t.Errorf("the restored mode has VMIN, VTIME = %d, %d, the cooked mode %d, %d", after.Cc[unix.VMIN], after.Cc[unix.VTIME], cooked.Cc[unix.VMIN], cooked.Cc[unix.VTIME])
			}
		})
	}
}
