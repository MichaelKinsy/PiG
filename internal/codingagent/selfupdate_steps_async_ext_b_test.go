package codingagent

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

type recordedSpawner struct {
	name string
	log  *[]string
	err  error
}

func (s recordedSpawner) Run() error {
	*s.log = append(*s.log, s.name)
	return s.err
}

// package-manager-cli.ts:734-756 runSelfUpdate awaits each step of `command.steps ?? [command]` in order and rejects at the first
// failing step: later steps never start, and the rejection names the failing step.
func TestRunPackageManagerStepsRunsStepsInOrderAndStopsAtTheFirstFailure(t *testing.T) {
	step := func(name string) *SelfUpdateCommand {
		return &SelfUpdateCommand{Command: name, Display: name + " display"}
	}
	boom := errors.New("exit status 7")
	run := func(cmd *SelfUpdateCommand, failing string) ([]string, error) {
		var log []string
		err := runPackageManagerSteps(cmd, func(name string, _ ...string) spawner {
			var failure error
			if name == failing {
				failure = boom
			}
			return recordedSpawner{name: name, log: &log, err: failure}
		})
		return log, err
	}
	steps := &SelfUpdateCommand{Command: "install", Display: "uninstall && install && verify", Steps: []*SelfUpdateCommand{step("uninstall"), step("install"), step("verify")}}
	log, err := run(steps, "")
	if err != nil || !slices.Equal(log, []string{"uninstall", "install", "verify"}) {
		t.Fatalf("all steps: ran %v, error %v", log, err)
	}
	log, err = run(steps, "install")
	if !slices.Equal(log, []string{"uninstall", "install"}) {
		t.Errorf("ran %v, want the step after the failing one skipped", log)
	}
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "install display") {
		t.Errorf("error = %v, want the failing step's display wrapping its failure", err)
	}
	single := step("only")
	if log, err = run(single, ""); err != nil || !slices.Equal(log, []string{"only"}) {
		t.Errorf("a command without steps: ran %v, error %v, want itself once", log, err)
	}
}
