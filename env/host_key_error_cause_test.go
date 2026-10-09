package env

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os/exec"
	"path/filepath"
	"testing"
)

// packages/env/src/ssh.ts:40 `class HostKeyUnknownError extends Error {}` and packages/env/src/ssh.ts:43
// `class HostKeyChangedError extends Error {}` inherit Error's constructor, so `new HostKeyUnknownError(message, { cause })`
// sets the `cause` property and ssh.ts's own `new HostKeyUnknownError(message)` leaves it undefined. In Go NewHostKeyUnknownError(message, ErrorOptions{Cause}) is the
// two-argument constructor; its Cause field is what errors.Unwrap, errors.Is and errors.As follow.
func TestHostKeyErrorsCarryTheCauseTheCallerPasses(t *testing.T) {
	root := &fs.PathError{Op: "open", Path: "/home/u/.ssh/known_hosts", Err: fs.ErrPermission}
	for name, e := range map[string]error{
		"HostKeyUnknownError": NewHostKeyUnknownError("not trusted", ErrorOptions{Cause: root}),
		"HostKeyChangedError": NewHostKeyChangedError("changed", ErrorOptions{Cause: root}),
	} {
		if cause := errors.Unwrap(e); !errors.Is(cause, root) {
			t.Errorf("%s cause = %v, want the PathError passed as cause", name, cause)
		}
		if !errors.Is(e, fs.ErrPermission) {
			t.Errorf("%s: errors.Is does not reach fs.ErrPermission through the cause", name)
		}
		var pathErr *fs.PathError
		if !errors.As(e, &pathErr) || pathErr.Path != "/home/u/.ssh/known_hosts" {
			t.Errorf("%s: errors.As does not find the PathError cause: %v", name, pathErr)
		}
	}
	if unknown := NewHostKeyUnknownError("not trusted", ErrorOptions{Cause: root}); !errors.Is(unknown.Cause, root) {
		t.Errorf("HostKeyUnknownError.Cause = %v, want the ErrorOptions cause", unknown.Cause)
	}
	if changed := NewHostKeyChangedError("changed", ErrorOptions{Cause: root}); !errors.Is(changed.Cause, root) {
		t.Errorf("HostKeyChangedError.Cause = %v, want the ErrorOptions cause", changed.Cause)
	}
	for name, e := range map[string]error{
		"HostKeyUnknownError": NewHostKeyUnknownError("The host key of example.test is not trusted yet"),
		"HostKeyChangedError": NewHostKeyChangedError("The host key of example.test changed; remove the old key to continue"),
	} {
		if cause := errors.Unwrap(e); cause != nil {
			t.Errorf("%s built as ssh.ts builds it has cause %v, want none", name, cause)
		}
	}
}

// hostKeyErrorOracle constructs both classes from Pi's own packages/env/src/ssh.ts (Node 24 runs it directly) without and with
// ErrorOptions and reports what `name`, `message` and `cause` hold.
const hostKeyErrorOracle = `
import { pathToFileURL } from "node:url";
const ssh = await import(pathToFileURL(process.argv[1]).href);
const root = new Error("root cause");
const out = {};
for (const name of ["HostKeyUnknownError", "HostKeyChangedError"]) {
  const bare = new ssh[name]("bare message");
  const caused = new ssh[name]("caused message", { cause: root });
  out[name] = { name: bare.name, message: bare.message, bareHasCause: "cause" in bare, causeIsTheOption: caused.cause === root, causedMessage: caused.message };
}
process.stdout.write(JSON.stringify(out));
`

type hostKeyErrorObservation struct {
	Name             string `json:"name"`
	Message          string `json:"message"`
	BareHasCause     bool   `json:"bareHasCause"`
	CauseIsTheOption bool   `json:"causeIsTheOption"`
	CausedMessage    string `json:"causedMessage"`
}

// The Pi oracle for packages/env/src/ssh.ts:40 and :43: Pi's classes keep the ErrorOptions cause and leave it unset otherwise, and
// the Go Cause field and Unwrap do the same.
func TestHostKeyErrorCauseMatchesThePiOracle(t *testing.T) {
	source, err := filepath.Abs("../.upstream/current/packages/env/src/ssh.ts")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", hostKeyErrorOracle, source)
	raw, err := cmd.Output()
	if err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("Pi oracle: %v\n%s", err, exit.Stderr)
		}
		t.Fatalf("Pi oracle: %v", err)
	}
	var pi map[string]hostKeyErrorObservation
	if err := json.Unmarshal(raw, &pi); err != nil {
		t.Fatalf("Pi oracle output %q: %v", raw, err)
	}
	root := errors.New("root cause")
	type goError interface {
		error
		Name() string
	}
	cases := map[string]struct{ bare, caused goError }{
		"HostKeyUnknownError": {NewHostKeyUnknownError("bare message"), NewHostKeyUnknownError("caused message", ErrorOptions{Cause: root})},
		"HostKeyChangedError": {NewHostKeyChangedError("bare message"), NewHostKeyChangedError("caused message", ErrorOptions{Cause: root})},
	}
	// causeField reads the Cause field, the `cause` property itself, besides the Unwrap chain.
	causeField := func(e error) error {
		if unknown, ok := errors.AsType[*HostKeyUnknownError](e); ok {
			return unknown.Cause
		}
		if changed, ok := errors.AsType[*HostKeyChangedError](e); ok {
			return changed.Cause
		}
		return nil
	}
	for name, c := range cases {
		want, ok := pi[name]
		if !ok {
			t.Fatalf("Pi oracle reported no %s: %s", name, raw)
		}
		got := hostKeyErrorObservation{
			Name: c.bare.Name(), Message: c.bare.Error(), BareHasCause: errors.Unwrap(c.bare) != nil,
			CauseIsTheOption: errors.Is(errors.Unwrap(c.caused), root) && errors.Is(causeField(c.caused), root), CausedMessage: c.caused.Error(),
		}
		if got != want {
			t.Errorf("%s cause: Go %+v, Pi %+v", name, got, want)
		}
	}
}
