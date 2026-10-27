package coding

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// mcpFakeScope is a connection whose extension is a function: it answers each callback the host sends, and holds the objects the host added.
type mcpFakeScope struct {
	mu       sync.Mutex
	serve    func(id string, args []json.RawMessage) (any, error)
	objects  map[string]subprocess.SessionGraphObject
	removed  []string
	next     int
	requests []string
}

var _ subprocess.SessionGraphScope = (*mcpFakeScope)(nil)

func (s *mcpFakeScope) Object(handle string) (subprocess.SessionGraphObject, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[handle]
	return object, ok
}

// Retain returns the object with a no-op release: the fake scope holds its objects for the whole test.
func (s *mcpFakeScope) Retain(handle string) (subprocess.SessionGraphObject, func(), bool) {
	object, ok := s.Object(handle)
	return object, func() {}, ok
}
func (s *mcpFakeScope) Callback(_ context.Context, id string, raw json.RawMessage) (json.RawMessage, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.requests = append(s.requests, id+" "+string(raw))
	s.mu.Unlock()
	result, err := s.serve(id, args)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (s *mcpFakeScope) Add(object subprocess.SessionGraphObject) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects == nil {
		s.objects = map[string]subprocess.SessionGraphObject{}
	}
	s.next++
	handle := fmt.Sprintf("sg%d", s.next)
	s.objects[handle] = object
	return handle, nil
}

func (s *mcpFakeScope) RegisterNativeProvider(context.Context, json.RawMessage, func(context.Context, *extension.NativeProvider) error) (json.RawMessage, func(), error) {
	return nil, nil, errors.New("the fake scope registers no Provider objects")
}

func (s *mcpFakeScope) Remove(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, handle)
	s.removed = append(s.removed, handle)
}

func (s *mcpFakeScope) sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}
