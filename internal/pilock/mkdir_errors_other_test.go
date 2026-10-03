//go:build !windows

package pilock

import (
	"encoding/json"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Unix mkdir failures that are not EEXIST. Go's fs.ErrExist also matches ENOTEMPTY, which Node reports as ENOTEMPTY and proper-lockfile passes through.
var nonEEXISTMkdirErrors = map[string]error{
	"EPERM":     syscall.EPERM,
	"EACCES":    syscall.EACCES,
	"EBUSY":     syscall.EBUSY,
	"ENOTEMPTY": syscall.ENOTEMPTY,
}

// The Node message prefix ("<code>: <uv_strerror>") for each nonEEXISTMkdirErrors entry.
var nonEEXISTMkdirMessages = map[string]string{
	"EPERM":     "EPERM: operation not permitted",
	"EACCES":    "EACCES: permission denied",
	"EBUSY":     "EBUSY: resource busy or locked",
	"ENOTEMPTY": "ENOTEMPTY: directory not empty",
}

var eexistMkdirErrors = map[string]error{
	"EEXIST": syscall.EEXIST,
}

// libuv names an errno it does not know "Unknown system error <errno>" in both uv_err_name and uv_strerror (src/uv-common.c uv__unknown_err_code), and Node's fs errors take their code and message from those two (src/api/exceptions.cc UVException), keeping the negated errno. EDQUOT is such an errno. The expectation comes from Node's util.getSystemErrorName and util.getSystemErrorMessage for the same errno.
func TestCompromisedErrorNamesAnErrnoLibuvDoesNotKnowAsNodeDoes(t *testing.T) {
	errno := -int(syscall.EDQUOT)
	const script = `const util = require('node:util');
const errno = Number(process.argv[1]);
console.log(JSON.stringify([util.getSystemErrorName(errno), util.getSystemErrorMessage(errno)]));`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, "--", strconv.Itoa(errno)).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var node [2]string
	if err := json.Unmarshal(out, &node); err != nil {
		t.Fatalf("Node output %q: %v", out, err)
	}
	if !strings.HasPrefix(node[0], "Unknown system error") {
		t.Fatalf("libuv names EDQUOT %q here, so it does not exercise an unknown errno", node[0])
	}
	message := node[0] + ": " + node[1] + ", stat '/p'"
	compromise := &CompromisedError{Cause: &fs.PathError{Op: "stat", Path: "/p", Err: syscall.EDQUOT}}
	if got := compromise.Error(); got != message {
		t.Fatalf("Error() = %q, want %q", got, message)
	}
	if want := "[Error: " + message + "] {\n  errno: " + strconv.Itoa(errno) + ",\n  code: 'ECOMPROMISED',\n  syscall: 'stat',\n  path: '/p'\n}"; compromise.Inspect() != want {
		t.Fatalf("Inspect() = %q, want %q", compromise.Inspect(), want)
	}
}
