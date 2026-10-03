// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright Joyent, Inc. and other Node contributors
// SPDX-FileCopyrightText: Copyright Node.js contributors
// SPDX-License-Identifier: MIT

//go:build windows

package nodespawn

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// requiredEnv is libuv's required_vars, sorted as libuv keeps it.
var requiredEnv = [...]string{"HOMEDRIVE", "HOMEPATH", "LOGONSERVER", "PATH", "SYSTEMDRIVE", "SYSTEMROOT", "TEMP", "USERDOMAIN", "USERNAME", "USERPROFILE", "WINDIR"}

// programEnv is the block libuv's make_program_env (src/win/process.c, libuv
// 1.52.1) builds from pairs, the "name=value" entries that Node passes in its
// order. It sorts them with the C runtime's qsort, comparing the text before
// each entry's first "=" with CompareStringOrdinal ignoring case (env_strncmp).
// That qsort is not stable, so the order of entries whose names compare equal,
// such as "A=one" and "A=B=two", is the one the C runtime gives, and the
// child's getenv sees the first of them. It then inserts, in sort order, each
// required variable that no entry names and PiG has, with PiG's value. An
// entry with a NUL leaves pairs as they are: Node's spawn throws for it
// (checkArguments) before libuv runs.
func programEnv(pairs []string) []string {
	for _, pair := range pairs {
		if strings.IndexByte(pair, 0) >= 0 {
			return pairs
		}
	}
	names := make([][]uint16, len(pairs))
	for i, pair := range pairs {
		name, _, _ := strings.Cut(pair, "=")
		names[i] = utf16.Encode([]rune(name))
	}
	block := make([]string, 0, len(pairs)+len(requiredEnv))
	next := 0
	for _, i := range crtSort(names) {
		for ; next < len(requiredEnv); next++ {
			c := compareEnvNames(utf16.Encode([]rune(requiredEnv[next])), names[i])
			if c > 0 {
				break
			}
			if c == 0 {
				next++
				break
			}
			block = appendRequired(block, requiredEnv[next])
		}
		block = append(block, pairs[i])
	}
	for ; next < len(requiredEnv); next++ {
		block = appendRequired(block, requiredEnv[next])
	}
	return block
}

// appendRequired appends the required variable name with PiG's value when PiG
// has it. libuv adds it when GetEnvironmentVariableW(name, NULL, 0) is not 0,
// which counts the terminating NUL, so an empty value is added too.
func appendRequired(block []string, name string) []string {
	if value, ok := os.LookupEnv(name); ok {
		return append(block, name+"="+value)
	}
	return block
}

var (
	procCompareStringOrdinal = windows.NewLazySystemDLL("kernel32.dll").NewProc("CompareStringOrdinal")
	procQsort                = windows.NewLazySystemDLL("ucrtbase.dll").NewProc("qsort")
)

// emptyUTF16 is the string an empty name passes to CompareStringOrdinal.
var emptyUTF16 [1]uint16

// compareEnvNames is libuv's env_strncmp for two names: CompareStringOrdinal
// ignoring case, minus CSTR_EQUAL, so a failed call compares as less. The
// names are converted to uintptr in the Call expression, which
// //go:uintptrescapes keeps alive and in place until the call returns, since a
// caller may pass a temporary.
func compareEnvNames(a, b []uint16) int {
	first, second := &emptyUTF16[0], &emptyUTF16[0]
	if len(a) > 0 {
		first = &a[0]
	}
	if len(b) > 0 {
		second = &b[0]
	}
	r, _, _ := procCompareStringOrdinal.Call(uintptr(unsafe.Pointer(first)), uintptr(len(a)), uintptr(unsafe.Pointer(second)), uintptr(len(b)), 1) //nolint:gosec // G103: x/sys has no CompareStringOrdinal wrapper; each pointer is converted in the call, which keeps its name alive, and the API reads len units from it.
	return int(r) - 2
}

// crtSortState is the state of the one running crtSort call: the address of
// the array qsort sorts and the names its elements index.
var crtSortState struct {
	sync.Mutex
	base  uintptr
	order []uintptr
	names [][]uint16
}

// crtCompare is the comparator crtSort passes to qsort. qsort calls it on the
// goroutine that called qsort, which holds crtSortState. It turns each element
// address back into an index of the pinned array instead of dereferencing it.
var crtCompare = sync.OnceValue(func() uintptr {
	return windows.NewCallbackCDecl(func(a, b uintptr) uintptr {
		state := &crtSortState
		size := unsafe.Sizeof(uintptr(0))
		i := state.order[(a-state.base)/size]
		j := state.order[(b-state.base)/size]
		return uintptr(compareEnvNames(state.names[i], state.names[j]))
	})
})

// crtSort returns the indexes of names in the order that the C runtime's
// qsort gives them with env_strncmp as the comparator, including its
// permutation of names that compare equal. node.exe links the C runtime
// statically and imports no C runtime DLL; its qsort is built from the same
// Universal C Runtime source as the qsort that ucrtbase.dll exports, so calling
// ucrtbase.dll gives Node's order without porting Microsoft's code.
func crtSort(names [][]uint16) []uintptr {
	order := make([]uintptr, len(names))
	for i := range order {
		order[i] = uintptr(i)
	}
	if len(order) < 2 {
		return order
	}
	var pinner runtime.Pinner
	pinner.Pin(&order[0])
	defer pinner.Unpin()
	state := &crtSortState
	state.Lock()
	defer state.Unlock()
	state.base = uintptr(unsafe.Pointer(&order[0])) //nolint:gosec // G103: qsort sorts the pinned array in place; the comparator maps its element addresses back to indexes.
	state.order, state.names = order, names
	defer func() { state.base, state.order, state.names = 0, nil, nil }()
	// qsort returns void. The pinner holds the array in place from the address
	// the comparator reads to the end of the call, and the conversion in the call
	// keeps it alive.
	_, _, _ = procQsort.Call(uintptr(unsafe.Pointer(&order[0])), uintptr(len(order)), unsafe.Sizeof(order[0]), crtCompare()) //nolint:gosec // G103: qsort sorts the pinned array in place.
	return order
}
