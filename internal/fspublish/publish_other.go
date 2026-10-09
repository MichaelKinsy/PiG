//go:build !linux

package fspublish

// linkRefused reports false: these platforms publish only by hard link.
func linkRefused(error) bool { return false }

func renameNoReplace(_, _ string, linkErr error) error { return linkErr }
