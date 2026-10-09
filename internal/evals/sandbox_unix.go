//go:build unix

package evals

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// chownTree gives path and everything below it to identity, without following symbolic links (harness.ts chownTree).
func chownTree(path string, identity sandboxIdentity) error {
	return filepath.WalkDir(path, func(entry string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(entry, identity.uid, identity.gid)
	})
}

// enterToolSandbox makes the agent process an unprivileged user (harness.ts enterToolSandbox). Pi drops its own
// process after the model runtime holds the credentials; PiG's Session is a child process, so the child starts with
// the sandbox identity and no supplementary groups, after the harness hands it the run's root.
func enterToolSandbox(command *exec.Cmd, root string, identity *sandboxIdentity) error {
	if identity == nil {
		return nil
	}
	if os.Getuid() != 0 || os.Geteuid() != 0 {
		return errors.New("The eval runner must start as root before entering the unprivileged tool sandbox.")
	}
	if err := chownTree(root, *identity); err != nil {
		return err
	}
	command.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(identity.uid), Gid: uint32(identity.gid), Groups: []uint32{}}}
	return nil
}
