//go:build !windows

package nodespawn

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"slices"
	"strconv"
	"syscall"
)

const (
	elfTypeExec = 2
	elfTypeDyn  = 3

	elfProgramInterpreter = 3 // PT_INTERP

	// elfMaxProgramHeaderBytes is the kernel's limit on the program header
	// table. Kernels before Linux 6.17 also limit it to the page size
	// (ELF_MIN_ALIGN); kernelReadsLargeProgramHeaders reports which applies.
	elfMaxProgramHeaderBytes = 65536

	// pathMax is the kernel's PATH_MAX, the longest PT_INTERP it reads.
	pathMax = 4096
)

// elfLayout is where an ELF handler of the kernel reads the header fields and
// program headers it checks: the struct elfhdr and elf_phdr of its word size.
// A handler reads its own layout whatever the file's EI_CLASS says.
type elfLayout struct {
	word      int // 4 or 8
	phoff     int
	phentsize int
	phnum     int
	phdrSize  int
	pOffset   int
	pFilesz   int
}

var (
	elfLayout32 = elfLayout{word: 4, phoff: 28, phentsize: 42, phnum: 44, phdrSize: 32, pOffset: 4, pFilesz: 16}
	elfLayout64 = elfLayout{word: 8, phoff: 32, phentsize: 54, phnum: 56, phdrSize: 56, pOffset: 8, pFilesz: 32}
)

func (l elfLayout) uint(order binary.ByteOrder, b []byte) uint64 {
	if l.word == 4 {
		return uint64(order.Uint32(b))
	}
	return order.Uint64(b)
}

// elfHandler is a binfmt_elf handler of the running kernel: the native one or
// the compat one (fs/compat_binfmt_elf.c), with the e_machine values its
// elf_check_arch accepts (arch/*/include/asm/elf.h) and the EI_CLASS it
// requires, 0 when elf_check_arch does not read it.
type elfHandler struct {
	layout   elfLayout
	machines []uint16
	class    byte
}

// elfHandlers are the ELF handlers of each GOARCH's kernel, chosen by
// kernelArch. A kernel without an entry accepts every header that passes the
// other checks in the layout of PiG's word size.
var elfHandlers = map[string][]elfHandler{
	// EM_X86_64 natively; EM_386 and EM_486 with IA32 emulation. The x32 ABI
	// (EM_X86_64 in the 32-bit layout) needs CONFIG_X86_X32_ABI, which
	// distribution kernels leave off.
	"amd64":    {{elfLayout64, []uint16{62}, 0}, {elfLayout32, []uint16{3, 6}, 0}},
	"386":      {{elfLayout32, []uint16{3, 6}, 0}},
	"arm64":    {{elfLayout64, []uint16{183}, 0}, {elfLayout32, []uint16{40}, 0}},
	"arm":      {{elfLayout32, []uint16{40}, 0}},
	"riscv64":  {{elfLayout64, []uint16{243}, 2}},
	"ppc64le":  {{elfLayout64, []uint16{21}, 2}},
	"ppc64":    {{elfLayout64, []uint16{21}, 2}},
	"s390x":    {{elfLayout64, []uint16{22}, 2}},
	"loong64":  {{elfLayout64, []uint16{258}, 2}},
	"mips64le": {{elfLayout64, []uint16{8}, 2}, {elfLayout32, []uint16{8}, 1}},
}

// elfErrno is the errno of the kernel's ELF handlers (load_elf_binary,
// fs/binfmt_elf.c) for file, a regular file whose head starts with the ELF
// magic, up to the point where they report an error of their own: ENOEXEC when
// every handler rejects the header, the program header table, or the
// PT_INTERP entry, the errno of open_exec for a program interpreter that
// cannot be opened, and 0 otherwise. The header is read from head, zero padded
// as the kernel's buffer is, in the byte order of the machine. A relative
// program interpreter is relative to the child's working directory cwd.
func elfErrno(file *os.File, head []byte, cwd string) syscall.Errno {
	var header [64]byte
	copy(header[:], head)
	order := binary.NativeEndian
	machine := order.Uint16(header[18:])
	handlers, known := elfHandlers[kernelArch()]
	if !known {
		layout := elfLayout64
		if strconv.IntSize == 32 {
			layout = elfLayout32
		}
		handlers = []elfHandler{{layout, []uint16{machine}, 0}}
	}
	for _, handler := range handlers {
		if handler.class != 0 && header[4] != handler.class {
			continue
		}
		if slices.Contains(handler.machines, machine) {
			return handler.errno(file, header[:], cwd)
		}
	}
	return syscall.ENOEXEC
}

// errno is the errno of load_elf_binary for the handler whose elf_check_arch
// accepts header: the type, load_elf_phdrs, and the first PT_INTERP.
func (h elfHandler) errno(file *os.File, header []byte, cwd string) syscall.Errno {
	order := binary.NativeEndian
	l := h.layout
	if typ := order.Uint16(header[16:]); typ != elfTypeExec && typ != elfTypeDyn {
		return syscall.ENOEXEC
	}
	// load_elf_phdrs: a table of the handler's entry size, of at most 64 KiB
	// and, on a kernel before Linux 6.17, a page, read whole.
	if int(order.Uint16(header[l.phentsize:])) != l.phdrSize {
		return syscall.ENOEXEC
	}
	size := l.phdrSize * int(order.Uint16(header[l.phnum:]))
	if size == 0 || size > elfMaxProgramHeaderBytes {
		return syscall.ENOEXEC
	}
	if size > os.Getpagesize() {
		errno, asked := probeExecve(file.Name(), cwd)
		if errno, decided := overPageErrno(errno, asked, kernelReadsLargeProgramHeaders()); decided {
			return errno
		}
	}
	phdrs := make([]byte, size)
	if !readAt(file, phdrs, l.uint(order, header[l.phoff:])) {
		return syscall.ENOEXEC
	}
	for phdr := range slices.Chunk(phdrs, l.phdrSize) {
		if order.Uint32(phdr) != elfProgramInterpreter {
			continue
		}
		filesz := l.uint(order, phdr[l.pFilesz:])
		if filesz > pathMax || filesz < 2 {
			return syscall.ENOEXEC
		}
		interpreter := make([]byte, filesz)
		if !readAt(file, interpreter, l.uint(order, phdr[l.pOffset:])) {
			// The kernel fails with the read's own errno.
			return 0
		}
		if interpreter[filesz-1] != 0 {
			return syscall.ENOEXEC
		}
		name, _, _ := bytes.Cut(interpreter, []byte{0})
		return openExecErrno(string(name), cwd)
	}
	return 0
}

// overPageErrno is the errno of execve for an ELF file whose program header
// table is larger than a page, and whether it is decided before the table is
// read. Only the running kernel knows whether it has the page limit, and what
// follows when it does not, so its answer to probeExecve is the errno whenever
// it could be asked, even when its release is older than Linux 6.17 (a
// backport, or a sandbox that reports its own release). When it could not be
// asked, a kernel before Linux 6.17 (readsLarge false) rejects the table with
// ENOEXEC, and a later kernel reads it.
func overPageErrno(probed syscall.Errno, asked, readsLarge bool) (syscall.Errno, bool) {
	if asked {
		return probed, true
	}
	if !readsLarge {
		return syscall.ENOEXEC, true
	}
	return 0, false
}

// openExecErrno is the errno of the kernel's open_exec(name) in the child's
// working directory cwd: what a stat gives, and EACCES for a file that is not
// an executable regular file. An empty name is the working directory itself.
func openExecErrno(name, cwd string) syscall.Errno {
	return executableErrno(inDirectory(cwd, startName(name)))
}

// readAt reports that file has len(buf) bytes at offset off and reads them.
func readAt(file *os.File, buf []byte, off uint64) bool {
	if off > math.MaxInt64 {
		return false
	}
	n, err := file.ReadAt(buf, int64(off))
	return err == nil && n == len(buf)
}
