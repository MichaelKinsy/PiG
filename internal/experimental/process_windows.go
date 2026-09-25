package experimental

import "syscall"

func internalProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, HideWindow: true}
}
