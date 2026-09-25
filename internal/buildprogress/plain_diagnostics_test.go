package buildprogress

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReporterPlainDiagnosticsStripTerminalControls(t *testing.T) {
	var out bytes.Buffer
	r := New(&out, false)
	defer r.Close()
	input := []byte("warning: \x1b[31mexternal member\x1b[0m\r\n\tdetail\n")
	n, err := r.Write(input)
	if err != nil || n != len(input) {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(input))
	}
	if want := "warning: external member\n\tdetail\n"; out.String() != want {
		t.Fatalf("plain diagnostic = %q; want %q", out.String(), want)
	}
}

func TestReporterPlainFailureStripsTerminalControls(t *testing.T) {
	var out bytes.Buffer
	r := New(&out, false)
	r.Handle(Event{Phase: "Resolving manifest", Step: "input.yaml"})
	cause := errors.New("open \x1b[31minput.yaml\x1b[0m: denied\r\n\tdetail")
	err := r.Failure(cause)
	if !errors.Is(err, cause) {
		t.Fatal("lost structured error cause")
	}
	if strings.ContainsAny(err.Error(), "\x1b\r") || !strings.Contains(err.Error(), "open input.yaml: denied\n\tdetail") {
		t.Fatalf("plain failure = %q", err)
	}
}
