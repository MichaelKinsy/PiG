package coding

import (
	"errors"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestNewInMemorySessionManagerFailsWithoutAWorkingDirectory(t *testing.T) {
	testenv.DeletedWorkingDirectory(t)
	if sm, err := NewInMemorySessionManager(""); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("NewInMemorySessionManager(\"\") = %v, %v; want the resolve error, as filepath.Abs gave", sm, err)
	}
	if sm, err := NewInMemorySessionManager("/work"); err != nil || sm.CWD() != "/work" {
		t.Fatalf("an absolute cwd never reads the working directory: %v, %v", sm, err)
	}
}
