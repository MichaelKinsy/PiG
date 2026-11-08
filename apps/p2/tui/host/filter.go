package host

// The only untrusted output path to the physical terminal. Each write is a complete, bounded frame.
//
// pi: apps/p2/src/hosts/tui-filter.ts

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// MaxTerminalFrame is MAX_TERMINAL_FRAME.
const MaxTerminalFrame = 64 * 1024

// ErrFrameTooLarge is the RangeError of an output frame over MaxTerminalFrame.
var ErrFrameTooLarge = errors.New("TUI output frame exceeds 64 KiB")

// BaseModes are the DEC private modes worker output may set (section 3.6): cursor, bracketed paste, sync, alt screen, autowrap.
var BaseModes = map[string]bool{"25": true, "2004": true, "2026": true, "1049": true, "7": true}

// MouseModes are mouse reporting and focus events, admitted only under the visitor's mouse policy; 1003 only with "all".
var MouseModes = []string{"1000", "1002", "1006", "1004"}

var (
	sgrBody       = lazyregexp.New(`^[\d;:]*$`)
	fiveDigits    = lazyregexp.New(`\d{5}`)
	numericBody   = lazyregexp.New(`^[\d;]*$`)
	eraseBody     = lazyregexp.New(`^[012]?$`)
	scrollRegBody = lazyregexp.New(`^(?:\d{1,4};\d{1,4})?$`)
)

func allowedCsi(body, final string, modes map[string]bool) bool {
	if len(body) > 128 {
		return false
	}
	if final == "h" || final == "l" {
		return strings.HasPrefix(body, "?") && modes[body[1:]]
	}
	if final == "m" {
		return sgrBody.MatchString(body) && !fiveDigits.MatchString(body)
	}
	if !numericBody.MatchString(body) || fiveDigits.MatchString(body) {
		return false
	}
	if strings.Contains("ABCDEFGHfd", final) {
		limit := 1
		if final == "H" || final == "f" {
			limit = 2
		}
		return strings.Count(body, ";")+1 <= limit
	}
	if final == "J" || final == "K" {
		return eraseBody.MatchString(body)
	}
	if final == "r" {
		return scrollRegBody.MatchString(body)
	}
	return (final == "s" || final == "u") && body == ""
}

// FilterTerminalOutput passes only bounded complete allowed sequences. No partial escapes survive a frame boundary; unterminated control
// strings consume the rest of that frame.
func FilterTerminalOutput(input string, modes map[string]bool) (string, error) {
	if len(input) > MaxTerminalFrame {
		return "", ErrFrameTooLarge
	}
	in := []rune(input)
	at := func(i int) rune {
		if i < len(in) && i >= 0 {
			return in[i]
		}
		return 0
	}
	var out strings.Builder
	for i := 0; i < len(in); i++ {
		code := in[i]
		if code == 27 || (code >= 0x80 && code <= 0x9f) {
			introducer := code
			hasIntroducer := true
			if code == 27 {
				i++
				hasIntroducer = i < len(in)
				introducer = at(i)
			}
			switch {
			case (introducer == '[' && hasIntroducer) || code == 0x9b:
				start := i + 1
				for i+1 < len(in) && in[i+1] >= 0x20 && in[i+1] <= 0x3f {
					i++
				}
				i++
				final := ""
				if i < len(in) {
					final = string(in[i])
				}
				end := min(i, len(in))
				body := string(in[min(start, end):end])
				if code == 27 && final != "" && allowedCsi(body, final, modes) {
					out.WriteString("\x1b[" + body + final)
				}
			case (hasIntroducer && strings.ContainsRune("]PX^_", introducer)) || code == 0x90 || code == 0x98 || code == 0x9d || code == 0x9e || code == 0x9f:
				for i++; i < len(in); i++ {
					if in[i] == 0x9c || in[i] == 7 {
						break
					}
					if in[i] == 27 && at(i+1) == '\\' && i+1 < len(in) {
						i++
						break
					}
				}
			case code == 27:
				// Consume a whole ESC sequence, including intermediates (e.g. charset selection).
				for i < len(in) && in[i] >= 0x20 && in[i] <= 0x2f {
					i++
				}
			}
			continue
		}
		if code == 9 || code == 10 || code == 13 || (code >= 0x20 && code != 0x7f) {
			out.WriteRune(code)
		}
	}
	return out.String(), nil
}
