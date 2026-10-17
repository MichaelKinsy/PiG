package contracttest

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// The fuzz kit: a core package fuzzes its encoder or scanner against the Go models of the reference with the vectors as seeds.
//
//	func FuzzEncode(f *testing.F) { contracttest.AddEncodeSeeds(f); f.Fuzz(contracttest.EncodeFuzz(encode)) }
//
// Run fuzzing only as the repository rule says: GOMAXPROCS=8 go test -fuzz=FuzzEncode -parallel=4 -fuzztime=30s. The seeds run as
// ordinary test cases under plain `go test`.

// AddEncodeSeeds adds the encoder vectors' inputs as seeds.
func AddEncodeSeeds(f *testing.F) {
	for _, v := range EncodeVectors(f) {
		f.Add([]byte(v.In))
	}
}

// EncodeFuzz returns a fuzz body: for any input that is JSON text, encode must write exactly what ReferenceEncode writes.
// Input that is not JSON is skipped: the core encodes what it wrote or what a host passed it, never arbitrary bytes.
func EncodeFuzz(encode func(in []byte) ([]byte, error)) func(*testing.T, []byte) {
	return func(t *testing.T, in []byte) {
		skip, err := encodeDifference(encode, in)
		if skip {
			t.Skip()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func encodeDifference(encode func(in []byte) ([]byte, error), in []byte) (skip bool, err error) {
	want, rerr := ReferenceEncode(in)
	if rerr != nil {
		return true, nil
	}
	got, err := encode(in)
	if err != nil {
		return false, fmt.Errorf("encode(%q): %w", in, err)
	}
	if !bytes.Equal(got, want) {
		return false, fmt.Errorf("encode(%q)\n want %q\n got  %q", in, want, got)
	}
	return false, nil
}

// AddScanSeeds adds the scanner vectors' records as seeds.
func AddScanSeeds(f *testing.F) {
	for _, v := range ScanVectors(f) {
		f.Add([]byte(v.Record))
	}
}

// ScanFuzz returns a fuzz body: for any record a JSON decoder reads into an index, scan must return the same index. Records that
// are not valid UTF-8 or whose strings hold a replacement character (a lone surrogate escape, which a Go decoder cannot keep) are skipped.
func ScanFuzz(scan func(record []byte) (Index, error)) func(*testing.T, []byte) {
	return func(t *testing.T, record []byte) {
		skip, err := scanDifference(scan, record)
		if skip {
			t.Skip()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func scanDifference(scan func(record []byte) (Index, error), record []byte) (skip bool, err error) {
	want, rerr := ReferenceIndex(record)
	if rerr != nil || !utf8.Valid(record) || strings.ContainsRune(want.Kind+want.Role+want.StopReason, utf8.RuneError) {
		return true, nil
	}
	got, err := scan(record)
	if err != nil {
		return false, fmt.Errorf("scan(%q): %w", record, err)
	}
	if !got.Equal(want) {
		return false, fmt.Errorf("scan(%q)\n want %+v\n got  %+v", record, want, got)
	}
	return false, nil
}
