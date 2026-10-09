package subprocess_test

import (
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// runSessionWrite answers "unsupported" when the replacement Session's manager does not satisfy sessionWriter, so a signature drift on codingagent.Session must fail the build instead of degrading a setup callback's appends.
var _ subprocess.SessionWriterForTest = (*codingagent.Session)(nil)
