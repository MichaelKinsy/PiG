package plugins

import (
	"crypto/rand"
	"encoding/base32"
	"os"
	"strings"
	"sync"
)

// The owner of a worker's socket directories (SocketOwnerEnv): who started this process, which names the directories its
// NodeHosts make so the process that started it can remove what it leaves.
var (
	socketOwnerMu sync.Mutex
	socketOwner   string
)

// NewSocketOwner is a value for SocketOwnerEnv unique to one start: 8 characters, short because it is part of every socket
// path, which a unix address holds only 104 bytes of (macOS).
func NewSocketOwner() string {
	random := make([]byte, 5)
	_, _ = rand.Read(random)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random))
}

// AdoptSocketOwner takes SocketOwnerEnv out of the environment, as the worker's channel variable is taken: this process's
// NodeHosts name their directories by it from now on, and no program this process starts sees it.
func AdoptSocketOwner() {
	owner := os.Getenv(SocketOwnerEnv)
	_ = os.Unsetenv(SocketOwnerEnv)
	if owner == "" {
		return
	}
	socketOwnerMu.Lock()
	defer socketOwnerMu.Unlock()
	socketOwner = owner
}

// setSocketOwner is AdoptSocketOwner of owner (for a test).
func setSocketOwner(owner string) {
	socketOwnerMu.Lock()
	defer socketOwnerMu.Unlock()
	socketOwner = owner
}

func currentSocketOwner() string {
	socketOwnerMu.Lock()
	defer socketOwnerMu.Unlock()
	return socketOwner
}
