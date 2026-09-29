package nodespawn

import (
	"runtime"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// kernelArch is the GOARCH whose ELF handlers the running kernel has, from
// the machine that uname reports: a 32-bit PiG may run on a 64-bit kernel,
// whose handlers accept its native programs. It is runtime.GOARCH when uname
// fails or names a machine without an entry in elfHandlers.
var kernelArch = sync.OnceValue(func() string {
	var name unix.Utsname
	if err := unix.Uname(&name); err != nil {
		return runtime.GOARCH
	}
	machine := unix.ByteSliceToString(name.Machine[:])
	switch {
	case machine == "x86_64":
		return "amd64"
	case machine == "i386" || machine == "i486" || machine == "i586" || machine == "i686":
		return "386"
	case machine == "aarch64":
		return "arm64"
	case strings.HasPrefix(machine, "armv"):
		return "arm"
	case machine == "loongarch64":
		return "loong64"
	case machine == "riscv64" || machine == "ppc64le" || machine == "ppc64" || machine == "s390x":
		return machine
	}
	return runtime.GOARCH
})

// kernelReadsLargeProgramHeaders reports whether the running kernel's
// load_elf_phdrs accepts a program header table larger than a page: Linux 6.17
// removed the ELF_MIN_ALIGN limit. It is false when uname fails.
var kernelReadsLargeProgramHeaders = sync.OnceValue(func() bool {
	var name unix.Utsname
	if err := unix.Uname(&name); err != nil {
		return false
	}
	return releaseAtLeast(unix.ByteSliceToString(name.Release[:]), 6, 17)
})

// releaseAtLeast reports whether the kernel release, as "6.17.0-1010-azure"
// spells it, is at least major.minor.
func releaseAtLeast(release string, major, minor int) bool {
	numbers := strings.FieldsFunc(release, func(r rune) bool { return r < '0' || r > '9' })
	if len(numbers) < 2 {
		return false
	}
	gotMajor, err := strconv.Atoi(numbers[0])
	if err != nil {
		return false
	}
	gotMinor, err := strconv.Atoi(numbers[1])
	if err != nil {
		return false
	}
	return gotMajor > major || gotMajor == major && gotMinor >= minor
}
