package subprocess

import "testing"

// TestReadyPayloadGeometryComesFromCallbacks pins that the ready payload
// carries real terminal geometry.
//
// SetHeightFunc originally shipped with no production call site, so
// ReadyPayload.Height was always 0 and every extension started believing the
// terminal had no height. The width fallback of 120 exists because extensions
// load before the TUI does; height has no such fallback and reports 0 until
// the wiring layer supplies a callback.
func TestReadyPayloadGeometryComesFromCallbacks(t *testing.T) {
	tests := []struct {
		name       string
		widthFn    func() int
		heightFn   func() int
		wantWidth  int
		wantHeight int
	}{
		{"both wired", func() int { return 200 }, func() int { return 60 }, 200, 60},
		{"height unwired reports zero", func() int { return 200 }, nil, 200, 0},
		{"width unwired falls back to 120", nil, func() int { return 60 }, 120, 60},
		{"zero width keeps the fallback", func() int { return 0 }, func() int { return 60 }, 120, 60},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHost(t.TempDir())
			if tc.widthFn != nil {
				h.SetWidthFunc(tc.widthFn)
			}
			if tc.heightFn != nil {
				h.SetHeightFunc(tc.heightFn)
			}

			gotWidth, gotHeight := h.readyGeometry()
			if gotWidth != tc.wantWidth {
				t.Errorf("ready width = %d, want %d", gotWidth, tc.wantWidth)
			}
			if gotHeight != tc.wantHeight {
				t.Errorf("ready height = %d, want %d", gotHeight, tc.wantHeight)
			}
		})
	}
}
