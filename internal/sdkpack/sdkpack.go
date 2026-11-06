// SPDX-License-Identifier: MIT

// Package sdkpack is the layout of the one blob an embedded SDK bundle carries: the SDK's files in staging order, each as its name, a NUL, its size in decimal, a NUL and its bytes. The blob is stored as one zstd frame (internal/zstdblob), so the whole SDK is decoded once, into one buffer, and every file is a slice of it.
//
// That layout is also what the staging hash covers: Hash is the digest pigsdk names a staged SDK by, "staging-v2", then each file's name, size and contents. A bundle's generator (automation/gen/packsdk) computes it from the files and embeds it, so a start that stages nothing decodes and hashes nothing.
package sdkpack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"strconv"
)

// ArchiveName is the file a bundle's Source holds: the zstd frame of the packed files.
const ArchiveName = "sdk.zst"

// hashDomain starts every staging hash.
const hashDomain = "staging-v2\x00"

// Append adds the file name with contents data to a packed blob.
func Append(blob []byte, name string, data []byte) []byte {
	blob = append(blob, name...)
	blob = append(blob, 0)
	blob = strconv.AppendInt(blob, int64(len(data)), 10)
	blob = append(blob, 0)
	return append(blob, data...)
}

// Hash is the staging hash of a packed blob: the first 16 hex digits of the SHA-256 of the domain tag and the blob.
func Hash(blob []byte) string {
	digest := sha256.New()
	digest.Write([]byte(hashDomain))
	digest.Write(blob)
	return hex.EncodeToString(digest.Sum(nil))[:16]
}

var errMalformed = errors.New("sdkpack: malformed blob")

// Walk calls yield for each file of a packed blob, in order. The data slices alias blob. A blob that does not parse, or a name that is not a relative slash path inside the SDK, fails the walk; yield's error stops it and is returned.
func Walk(blob []byte, yield func(name string, data []byte) error) error {
	for len(blob) > 0 {
		end := bytes.IndexByte(blob, 0)
		if end <= 0 {
			return errMalformed
		}
		name := string(blob[:end])
		if !fs.ValidPath(name) || name == "." {
			return errors.New("sdkpack: invalid file name " + strconv.Quote(name))
		}
		blob = blob[end+1:]
		end = bytes.IndexByte(blob, 0)
		if end <= 0 || end > 10 {
			return errMalformed
		}
		size := 0
		for _, digit := range blob[:end] {
			if digit < '0' || digit > '9' {
				return errMalformed
			}
			size = size*10 + int(digit-'0')
		}
		blob = blob[end+1:]
		if size > len(blob) {
			return errMalformed
		}
		if err := yield(name, blob[:size:size]); err != nil {
			return err
		}
		blob = blob[size:]
	}
	return nil
}
