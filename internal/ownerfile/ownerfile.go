// Package ownerfile makes files accessible to their owner only and checks
// that they are: mode 0600 and no group or other bits on POSIX, and on
// Windows, which has no mode bits, a DACL (D68).
package ownerfile
