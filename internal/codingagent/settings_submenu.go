package codingagent

import (
	"fmt"
	"maps"

	"github.com/MichaelKinsy/PiG/tui"
)

// SteppedSubmenuStep resolves its title, description, and options from earlier selections.
// Functions represent both constant and context-dependent upstream strings.
type SteppedSubmenuStep struct {
	Key         string
	Title       func(map[string]string) string
	Description func(map[string]string) string
	Options     func(map[string]string) []tui.SelectItem
	Preselect   func(map[string]string) string
	Layout      tui.SelectSubmenuOptions
}

// SteppedSubmenuOptions controls the initial step and completion behavior.
type SteppedSubmenuOptions struct {
	StartAtStep    int
	InitialContext map[string]string
	Loop           bool
}

// SteppedSubmenu selects dependent values, goes back one step on Escape, and optionally loops after completion.
type SteppedSubmenu struct {
	steps           []SteppedSubmenuStep
	onComplete      func(map[string]string)
	onCancel        func()
	opts            SteppedSubmenuOptions
	context         map[string]string
	stepIndex       int
	activeComponent *tui.SelectSubmenuComponent
}

func NewSteppedSubmenu(steps []SteppedSubmenuStep, onComplete func(map[string]string), onCancel func(), opts SteppedSubmenuOptions) *SteppedSubmenu {
	s := &SteppedSubmenu{steps: steps, onComplete: onComplete, onCancel: onCancel, opts: opts, context: maps.Clone(opts.InitialContext)}
	if s.context == nil {
		s.context = map[string]string{}
	}
	s.buildStep(opts.StartAtStep)
	return s
}

func (s *SteppedSubmenu) buildStep(index int) {
	s.stepIndex = index
	step := s.steps[index]
	description := step.Description(s.context)
	if len(s.steps) > 1 {
		description = fmt.Sprintf("Step %d/%d · %s", index+1, len(s.steps), description)
	}
	current := ""
	if step.Preselect != nil {
		current = step.Preselect(s.context)
	}
	s.activeComponent = tui.NewSelectSubmenu(step.Title(s.context), description, step.Options(s.context), current, step.Layout)
}

func (s *SteppedSubmenu) Render(width int) []string { return s.activeComponent.Render(width) }
func (s *SteppedSubmenu) Invalidate()               { s.activeComponent.Invalidate() }
func (s *SteppedSubmenu) HandleInput(data string) {
	s.activeComponent.HandleInput(data)
	if !s.activeComponent.Done() {
		return
	}
	step := s.steps[s.stepIndex]
	if s.activeComponent.Cancelled() {
		if s.stepIndex == 0 {
			s.onCancel()
			return
		}
		delete(s.context, step.Key)
		s.buildStep(s.stepIndex - 1)
		return
	}
	s.context[step.Key] = s.activeComponent.SelectedValue()
	if s.stepIndex < len(s.steps)-1 {
		s.buildStep(s.stepIndex + 1)
		return
	}
	s.onComplete(maps.Clone(s.context))
	if s.opts.Loop {
		s.context = map[string]string{}
		s.buildStep(0)
	} else {
		s.onCancel()
	}
}
