package host

import "github.com/MichaelKinsy/PiG/chord/next/internal/admitseam"

func init() {
	admitseam.Hold = func(host any, hold func(start func())) {
		h := host.(*chordHost)
		h.enter(func() { h.admissionHold = hold })
	}
}
