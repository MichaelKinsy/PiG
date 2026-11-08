package imageprocessing

import (
	"encoding/base64"
	"runtime"
	"testing"
)

// pi: packages/coding-agent/test/image-resize-worker-teardown.test.ts (Pi main 1f7c685b8, #10695). Pi resizes in a worker thread and must not resolve
// resizeImage while the worker it started is still terminating. ResizeImage runs in the caller with no worker, so the obligation is that a call
// returns with nothing of its own still running: serial calls leave the goroutine count where it was.
func TestSerialResizesLeaveNoWorkerRunning(t *testing.T) {
	const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="
	bytes, err := base64.StdEncoding.DecodeString(tinyPNG)
	if err != nil {
		t.Fatal(err)
	}
	before := runtime.NumGoroutine()
	for i := range 2 {
		result, err := ResizeImage(bytes, "image/png", nil)
		if err != nil || result.Data == "" {
			t.Fatalf("resize %d = %+v, %v", i, result, err)
		}
		if got := runtime.NumGoroutine(); got != before {
			t.Fatalf("after resize %d: %d goroutines, want %d", i, got, before)
		}
	}
}
