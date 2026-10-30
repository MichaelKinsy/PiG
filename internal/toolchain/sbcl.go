package toolchain

import (
	"os"
	"strings"
)

// pig additive (D19): PiG runs Common Lisp extensions, which Pi does not, and every image build and launch resolves SBCL here.
//
// SBCLExecutable returns the Steel Bank Common Lisp executable: PIG_SBCL when set, else the first of sbcl and sbcl.exe
// that lookPath finds. When none is found it returns the bare name, so the launch reports the missing toolchain.
func SBCLExecutable(goos string, lookPath func(string) (string, error)) string {
	if explicit := strings.TrimSpace(os.Getenv("PIG_SBCL")); explicit != "" {
		return explicit
	}
	name := "sbcl"
	if goos == "windows" {
		name = "sbcl.exe"
	}
	if path, err := lookPath(name); err == nil {
		return path
	}
	return name
}
