//go:build parity

package runner

// Startup transcripts recorded on xd6702 on 2026-11-16 with the transcript attached before the program ran (scenario 00-startup-banner of the startup family, 100x35, 20 runs per binary, car24-head): the bytes each binary wrote from its first write up to the first frame (CSI ? 2026 h).
//
// PiG wrote the same 269 bytes in 20 of 20 runs. Pi wrote 11 distinct prefixes: in 11 of 20 runs the 253 bytes below, and in 9 of 20 these bytes followed by CSI > 4 ; 2 m and an OSC 0 window title (the title ends in the run's random working directory name), which Pi writes when the terminal's answers to its queries arrive and which in the other 11 runs came after the first frame.
const (
	recordedPigStartup = "\x1b[?2004h\x1b[>7u\x1b[?u\x1b]7501;?\x1b\\\x1b[c\x1b[?25l\x1b[>4;2m\x1b[<u\x1b[?1049h\x1b[>7u\x1b[?7l\x1b[?1000h\x1b[?1002h\x1b[?1004h\x1b[?1006h\x1b[2J\x1b[H\x1b[?25l\x1b[?2031h\x1b]10;?\a\x1b]11;?\a\x1b]4;0;?\a\x1b]4;1;?\a\x1b]4;2;?\a\x1b]4;3;?\a\x1b]4;4;?\a\x1b]4;5;?\a\x1b]4;6;?\a\x1b]4;7;?\a\x1b]4;8;?\a\x1b]4;9;?\a\x1b]4;10;?\a\x1b]4;11;?\a\x1b]4;12;?\a\x1b]4;13;?\a\x1b]4;14;?\a\x1b]4;15;?\a\x1b[c"

	recordedPiStartup = "\x1b[?1049h\x1b[?7l\x1b[?1000h\x1b[?1002h\x1b[?1004h\x1b[?1006h\x1b[2J\x1b[H\x1b[?25l\x1b[?2004h\x1b[>7u\x1b[?u\x1b]7501;?\x1b\\\x1b[c\x1b[?25l\x1b[?2031h\x1b]10;?\a\x1b]11;?\a\x1b]4;0;?\a\x1b]4;1;?\a\x1b]4;2;?\a\x1b]4;3;?\a\x1b]4;4;?\a\x1b]4;5;?\a\x1b]4;6;?\a\x1b]4;7;?\a\x1b]4;8;?\a\x1b]4;9;?\a\x1b]4;10;?\a\x1b]4;11;?\a\x1b]4;12;?\a\x1b]4;13;?\a\x1b]4;14;?\a\x1b]4;15;?\a\x1b[c"

	// The pair Pi wrote after the first query reply in 9 of 20 runs, with two of the nine titles.
	recordedPiLateKeys = "\x1b[>4;2m"
	recordedPiTitleOne = "\x1b]0;\xcf\x80 - parity-snap-cwd-3978917759\a"
	recordedPiTitleTwo = "\x1b]0;\xcf\x80 - parity-snap-cwd-3693842136\a"
)
