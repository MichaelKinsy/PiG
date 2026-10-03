// Ports packages/durable/src/harness/registry.ts.

package harness

import (
	"fmt"
	"regexp"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/durable"
)

var sectionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

const sectionKeySource = `/^[a-z][a-z0-9_-]*$/`

// RegistryReader is the read side of a registry consumed by a Harness.
type RegistryReader interface {
	// Snapshot returns an immutable view of the whole current registry.
	Snapshot() durable.RegistrySnapshot
	// Subscribe registers a listener called synchronously after every publication; it wakes the scheduler to reconsider blocked tasks. The result unsubscribes.
	Subscribe(listener func()) func()
}

// Registry is an application-owned registry of extensions.
type Registry interface {
	RegistryReader
	// Install installs extension, or replaces the installed extension with its name in place, and publishes at once. An invalid extension, or a task name collision in the resulting registry, publishes nothing and returns the error.
	Install(extension *durable.Extension) error
	// Uninstall removes the installed extension with extension.Name, whichever object it is. A later install appends.
	Uninstall(extension *durable.Extension)
}

// BuiltinTasks are the built-in task definitions every registry holds; they are not an extension and cannot be removed or replaced.
func BuiltinTasks() []durable.AnyTask {
	return []durable.AnyTask{GenerationTask, ToolTask, CompactionTask}
}

// registryState is one immutable published registry state.
type registryState struct {
	extensions []*durable.Extension
	byName     map[string]*durable.Extension
	taskOrder  []durable.AnyTask
	tasks      map[string]durable.AnyTask
}

func newRegistryState(extensions []*durable.Extension) (*registryState, error) {
	state := &registryState{
		extensions: extensions,
		byName:     make(map[string]*durable.Extension, len(extensions)),
		tasks:      map[string]durable.AnyTask{},
	}
	for _, extension := range extensions {
		state.byName[extension.Name] = extension
	}
	for _, task := range BuiltinTasks() {
		state.addTask(task)
	}
	for _, extension := range extensions {
		for _, task := range extension.Tasks {
			name := task.AnyDefinition().Name
			if _, exists := state.tasks[name]; exists {
				return nil, fmt.Errorf("Task %s of extension %s is already installed", name, extension.Name)
			}
			state.addTask(task)
		}
	}
	return state, nil
}

func (state *registryState) addTask(task durable.AnyTask) {
	state.tasks[task.AnyDefinition().Name] = task
	state.taskOrder = append(state.taskOrder, task)
}

func (state *registryState) Installed() []*durable.Extension { return slices.Clone(state.extensions) }

func (state *registryState) Extension(name string) *durable.Extension { return state.byName[name] }

func (state *registryState) Tools() []durable.RegistryTool {
	var tools []durable.RegistryTool
	for _, extension := range state.extensions {
		for _, tool := range extension.Tools {
			tools = append(tools, durable.RegistryTool{Extension: extension, Tool: tool})
		}
	}
	return tools
}

func (state *registryState) Sections() []durable.RegistrySection {
	var sections []durable.RegistrySection
	for _, extension := range state.extensions {
		for _, section := range extension.Sections {
			sections = append(sections, durable.RegistrySection{Extension: extension, Section: section})
		}
	}
	return sections
}

func (state *registryState) Tasks() []durable.AnyTask { return slices.Clone(state.taskOrder) }

func (state *registryState) Task(name string) durable.AnyTask { return state.tasks[name] }

type registryListener struct{ call func() }

type registryImpl struct {
	mu        sync.Mutex
	current   *registryState
	listeners []*registryListener
}

// CreateRegistry creates an application-owned registry holding only the built-in tasks.
func CreateRegistry() Registry {
	state, err := newRegistryState(nil)
	if err != nil {
		panic(err)
	}
	return &registryImpl{current: state}
}

func (registry *registryImpl) Snapshot() durable.RegistrySnapshot {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.current
}

func (registry *registryImpl) Subscribe(listener func()) func() {
	entry := &registryListener{call: listener}
	registry.mu.Lock()
	registry.listeners = append(registry.listeners, entry)
	registry.mu.Unlock()
	return func() {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		registry.listeners = slices.DeleteFunc(registry.listeners, func(candidate *registryListener) bool { return candidate == entry })
	}
}

func (registry *registryImpl) Install(extension *durable.Extension) error {
	if err := validateExtension(extension); err != nil {
		return err
	}
	registry.mu.Lock()
	current := registry.current.extensions
	index := slices.IndexFunc(current, func(installed *durable.Extension) bool { return installed.Name == extension.Name })
	next := slices.Clone(current)
	if index < 0 {
		next = append(next, extension)
	} else {
		next[index] = extension
	}
	return registry.publishLocked(next)
}

func (registry *registryImpl) Uninstall(extension *durable.Extension) {
	registry.mu.Lock()
	current := registry.current.extensions
	if !slices.ContainsFunc(current, func(installed *durable.Extension) bool { return installed.Name == extension.Name }) {
		registry.mu.Unlock()
		return
	}
	next := slices.DeleteFunc(slices.Clone(current), func(installed *durable.Extension) bool { return installed.Name == extension.Name })
	// Removing an extension cannot create a task name collision.
	_ = registry.publishLocked(next)
}

// publishLocked builds and validates the next state, which fails on a task name collision, then publishes it and calls the listeners synchronously after releasing the lock.
func (registry *registryImpl) publishLocked(extensions []*durable.Extension) error {
	state, err := newRegistryState(extensions)
	if err != nil {
		registry.mu.Unlock()
		return err
	}
	registry.current = state
	listeners := slices.Clone(registry.listeners)
	registry.mu.Unlock()
	for _, listener := range listeners {
		listener.call()
	}
	return nil
}

// validateExtension checks unique tool names and section keys within one extension, and valid, unreserved section keys.
func validateExtension(extension *durable.Extension) error {
	tools := map[string]bool{}
	for _, tool := range extension.Tools {
		if tools[tool.Name] {
			return fmt.Errorf("Extension %s has two tools named %s", extension.Name, tool.Name)
		}
		tools[tool.Name] = true
	}
	sections := map[string]bool{}
	for _, section := range extension.Sections {
		key := section.Key
		if !sectionKeyPattern.MatchString(key) {
			return &TypeError{Message: fmt.Sprintf("Section key %s must match %s", jsonQuote(key), sectionKeySource)}
		}
		if key == InstructionsKey {
			return fmt.Errorf("Section key %s is reserved for the agent's instructions", key)
		}
		if sections[key] {
			return fmt.Errorf("Extension %s has two sections with key %s", extension.Name, key)
		}
		sections[key] = true
	}
	return nil
}

// TypeError is upstream's TypeError: a value of the wrong shape.
type TypeError struct{ Message string }

func (err *TypeError) Error() string { return err.Message }
