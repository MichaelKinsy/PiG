package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/node/app/uploads.test.ts: chunked uploads on their own (slice 13 §4): the cap of uploads
// under way, a slot freed by expiry or by the object's end, the files of ended uploads removed. Their use through
// `Files` (pipelined appends, finish waiting for writes, caps) is in files_test.go. Each upload carries its owner's
// data, found and given back.

func chordCode(err error) (data.ChordErrorCode, data.ChordErrorReason) {
	if chordErr, ok := errors.AsType[*data.ChordError](err); ok {
		return chordErr.Code, chordErr.Reason
	}
	return "", ""
}

func TestUploadsUnderWayRefuseABeginPastTheCapBusyAnExpiredUploadFreesItsSlotAndItsFile(t *testing.T) {
	run := filepath.Join(t.TempDir(), "run")
	uploads := NewUploads[string](run, 1)
	first, err := uploads.Begin("a")
	if err != nil {
		t.Fatal(err)
	}
	if code, reason := chordCode(func() error { _, err := uploads.Begin("b"); return err }()); code != data.CodeUnavailable || reason != data.ReasonBusy {
		t.Fatalf("second Begin = %v/%v, want unavailable/busy", code, reason)
	}
	if err := uploads.Expire(60 * time.Second); err != nil {
		t.Fatal(err)
	}
	if size, err := uploads.Uploaded(first); err != nil || size != 0 {
		t.Fatalf("Uploaded = %d, %v", size, err)
	}
	if err := uploads.Expire(0); err != nil {
		t.Fatal(err)
	}
	if _, err := uploads.Uploaded(first); err == nil {
		t.Fatal("an expired upload is still there")
	} else if code, _ := chordCode(err); code != data.CodeInvalid {
		t.Fatalf("code = %v, want invalid", code)
	}
	second, err := uploads.Begin("b")
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, run); len(got) != 1 || got[0] != second {
		t.Fatalf("run = %v, want [%s]", got, second)
	}
	if err := uploads.Close(); err != nil {
		t.Fatal(err)
	}
	if got := names(t, run); len(got) != 0 {
		t.Fatalf("run after close = %v", got)
	}
	if id, err := uploads.Begin("c"); err != nil || id == "" {
		t.Fatalf("Begin after Close = %q, %v", id, err)
	}
	if err := uploads.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUploadsFindAnUploadUnderWayByItsDataAndGiveTheDataBackWhenItFinishes(t *testing.T) {
	type owner struct{ key string }
	run := filepath.Join(t.TempDir(), "run")
	uploads := NewUploads[owner](run, 4)
	id, err := uploads.Begin(owner{key: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	if found, ok := uploads.Find(func(d owner) bool { return d.key == "k1" }); !ok || found != id {
		t.Fatalf("Find k1 = %q, %v", found, ok)
	}
	if _, ok := uploads.Find(func(d owner) bool { return d.key == "k2" }); ok {
		t.Fatal("Find k2 found an upload")
	}
	if err := uploads.Append(id, 0, []byte("abc"), func(int64, int64) error { return nil }); err != nil {
		t.Fatal(err)
	}
	finished, err := uploads.Finish(id, filepath.Join(run, "done"))
	if err != nil || finished.Data.key != "k1" || finished.Size != 3 {
		t.Fatalf("Finish = %+v, %v", finished, err)
	}
	if _, ok := uploads.Find(func(d owner) bool { return d.key == "k1" }); ok {
		t.Fatal("a finished upload is still found")
	}
	// A writer that gives up: its upload and its file go.
	dropped, err := uploads.Begin(owner{key: "k3"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := uploads.Cancel(dropped); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := uploads.Find(func(d owner) bool { return d.key == "k3" }); ok {
		t.Fatal("a cancelled upload is still found")
	}
	if got := strings.Join(names(t, run), ","); got != "done" {
		t.Fatalf("run = %q", got)
	}
	if err := uploads.Close(); err != nil {
		t.Fatal(err)
	}
}
