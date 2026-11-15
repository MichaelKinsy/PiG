//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package sdk

// openNoFollow is 0 where the platform has no flag that refuses a symbolic link: Windows keeps its temporary directory private to the user.
const openNoFollow = 0
