package subprocess

import (
	"os/exec"
)

// registrationSeen is what the host holds of an extension's registration, and registrationOf, setRegistrationFor, joinProcessFor, clearProcessFor and processLinkOf are
// the places the tests below and state_push_test.go reach it through.
type registrationSeen struct {
	flagNames          []string
	flagDefaults       map[string]any
	wantsSessionLog    bool
	wantsModelRegistry bool
}

func registrationOf(me *managedExt) registrationSeen {
	return registrationSeen{me.flagNames, me.flagDefaults, me.wantsSessionLog, me.wantsModelRegistry}
}

func setRegistrationFor(me *managedExt, r registrationSeen) {
	me.flagNames, me.flagDefaults, me.wantsSessionLog, me.wantsModelRegistry = r.flagNames, r.flagDefaults, r.wantsSessionLog, r.wantsModelRegistry
}

// joinProcessFor records that me runs in the process of origin, as adoptProcess does.
func joinProcessFor(me *managedExt, share *processShare, origin *managedExt, cmd *exec.Cmd, exited <-chan struct{}) {
	me.share, me.procOwner = share, origin
	me.setProcessHandle(nil, nil, cmd, exited)
}

// clearProcessFor forgets the process me ran in, as a start does before it spawns or adopts the next.
func clearProcessFor(me *managedExt) {
	me.setProcessHandle(nil, nil, nil, nil)
	me.share, me.procOwner = nil, nil
}

// processLinkOf reads the handle and the link of me's process under the guard of the handle.
func processLinkOf(me *managedExt) (share *processShare, origin *managedExt, cmd *exec.Cmd, exited <-chan struct{}) {
	me.procMu.Lock()
	defer me.procMu.Unlock()
	return me.share, me.procOwner, me.cmd, me.exitedCh
}
