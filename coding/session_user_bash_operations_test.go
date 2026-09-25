package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// recordingOperations stands in for an extension's remote execution.
type recordingOperations struct {
	command, cwd string
}

func (r *recordingOperations) Exec(_ context.Context, command, cwd string, options extension.BashOperationsExecOptions) (extension.BashOperationsResult, error) {
	r.command, r.cwd = command, cwd
	options.OnData([]byte("remote output\n"))
	code := 3
	return extension.BashOperationsResult{ExitCode: &code}, nil
}

// User bash runs through operations a user_bash handler supplied, with the
// command prefix applied and the result recorded (upstream executeBash's
// options.operations).
func TestExecuteBashWithOperationsUsesExtensionOperations(t *testing.T) {
	sess := newBashTestSession(t, `{"shellCommandPrefix":"set -e"}`)
	ops := &recordingOperations{}
	var chunks []string
	result, err := sess.ExecuteBashWithOperations(context.Background(), "ls", false, func(c string) { chunks = append(chunks, c) }, ops)
	if err != nil {
		t.Fatal(err)
	}
	if ops.command != "set -e\nls" || ops.cwd != sess.CWD() {
		t.Fatalf("operations got command %q cwd %q", ops.command, ops.cwd)
	}
	if result.Output != "remote output\n" || result.ExitCode != 3 || len(chunks) != 1 {
		t.Fatalf("result = %+v chunks = %q", result, chunks)
	}
}
