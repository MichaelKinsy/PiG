package fspublish

import (
	"io"
	"os"
)

// Duplicate makes the file at src also available at dst, which must not exist. It links src to dst. Where hard links are refused, as in Termux, it copies src to a new file at dst with src's permissions and syncs it. A failed copy leaves no file at dst.
func Duplicate(src, dst string) error {
	err := link(src, dst)
	if err == nil || !linkRefused(err) {
		return err
	}
	return copyNew(src, dst)
}

// copyNew copies src to dst, which it creates exclusively.
func copyNew(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}
