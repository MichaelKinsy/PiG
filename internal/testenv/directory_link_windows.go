//go:build windows

package testenv

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// createDirectoryLink creates newname as a directory junction to oldname, as Node's fs.symlinkSync(target, path, "junction") does: a relative target resolves against the link's parent directory, as a symbolic link's does, the junction stores it as an absolute path, and creating it needs no privilege.
func createDirectoryLink(oldname, newname string) error {
	target := oldname
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(newname), target)
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.Mkdir(newname, 0o777); err != nil {
		return err
	}
	if err := setMountPoint(newname, target); err != nil {
		return &os.LinkError{Op: "junction", Old: oldname, New: newname, Err: errors.Join(err, os.Remove(newname))}
	}
	return nil
}

// setMountPoint writes an IO_REPARSE_TAG_MOUNT_POINT reparse point to the empty directory link. Its REPARSE_DATA_BUFFER holds the tag, the data length and a reserved field, then the substitute and print name offsets and byte lengths, then both names, each followed by a NUL.
func setMountPoint(link, target string) error {
	// Win32 namespaced targets use the NT object-manager prefix in a reparse record, not a second prefix before \\?\.
	target = strings.TrimPrefix(target, `\\?\`)
	substitute, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		return err
	}
	printName, err := windows.UTF16FromString(target)
	if err != nil {
		return err
	}
	names := 2 * (len(substitute) + len(printName))
	if 16+names > windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE {
		return windows.ERROR_FILENAME_EXCED_RANGE
	}
	buffer := make([]byte, 16+names)
	le := binary.LittleEndian
	le.PutUint32(buffer[0:], windows.IO_REPARSE_TAG_MOUNT_POINT)
	le.PutUint16(buffer[4:], uint16(8+names))
	le.PutUint16(buffer[10:], uint16(2*(len(substitute)-1)))
	le.PutUint16(buffer[12:], uint16(2*len(substitute)))
	le.PutUint16(buffer[14:], uint16(2*(len(printName)-1)))
	for i, unit := range append(substitute, printName...) {
		le.PutUint16(buffer[16+2*i:], unit)
	}
	path, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	var returned uint32
	err = windows.DeviceIoControl(handle, windows.FSCTL_SET_REPARSE_POINT, &buffer[0], uint32(len(buffer)), nil, 0, &returned, nil)
	return errors.Join(err, windows.CloseHandle(handle))
}
