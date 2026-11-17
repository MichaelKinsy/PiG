//go:build darwin || dragonfly || freebsd || netbsd || openbsd

// SPDX-License-Identifier: MIT

package workers

// fionread is the request for the bytes a stream holds: FIONREAD, _IOR('f', 127, int) in sys/filio.h of Darwin and the BSDs
// (x/sys and syscall do not name it).
const fionread = 0x4004667f
