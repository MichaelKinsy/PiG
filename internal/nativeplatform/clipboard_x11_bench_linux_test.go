//go:build linux

package nativeplatform

import (
	"bytes"
	"os"
	"testing"
)

func BenchmarkNativeX11IncrementalImage(b *testing.B) {
	server := startNativeTestProcess(b, "Xvfb", []string{"-displayfd", "1", "-screen", "0", "640x480x24", "-nolisten", "tcp"}, os.Environ())
	display := ":" + nativeReady(b, server)
	b.Setenv("DISPLAY", display)
	data := bytes.Repeat([]byte{123}, 4*1024*1024)
	writeXclipTest(b, os.Environ(), "image/png", data)
	clipboard := GetNativeClipboard()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		got, available, err := clipboard.GetImage(b.Context())
		if err != nil || !available || !bytes.Equal(got, data) {
			b.Fatalf("transfer=%d available=%t err=%v", len(got), available, err)
		}
	}
}
