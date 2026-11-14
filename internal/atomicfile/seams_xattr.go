//go:build linux || darwin

package atomicfile

var listAttributes = func(string) ([]string, error) { return nil, nil }
