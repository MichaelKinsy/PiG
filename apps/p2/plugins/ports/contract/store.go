package contract

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports apps/p2/plugins/ports/protocol.ts (validation) and store.ts (the stored forwards).

const (
	idAlphabet  = "abcdefghijklmnopqrstuvwxyz0123456789"
	maxForwards = 50
)

// Forward is a stored forward (`forwards.json`): Name is empty without one, Session when the owner added it.
type Forward struct {
	ID      string
	Port    int
	Name    string
	Public  bool
	Session string
}

// ValidateForwardPort is `validateForwardPort` for a value read from JSON.
func ValidateForwardPort(value any) (int, error) {
	number, ok := value.(float64)
	if !ok || number != math.Trunc(number) || number < 1 || number > 65535 {
		return 0, errors.New("Port must be a number from 1 to 65535")
	}
	return int(number), nil
}

// validPort is ValidateForwardPort for a typed port.
func validPort(port int) error {
	_, err := ValidateForwardPort(float64(port))
	return err
}

// ValidateForwardName is `validateForwardName` for a name that may be absent: the trimmed name, "" when absent or blank.
func ValidateForwardName(name *string) (string, error) {
	if name == nil {
		return "", nil
	}
	return validName(*name)
}

func validName(name string) (string, error) {
	tooLong := len(utf16.Encode([]rune(name))) > MaxForwardName
	if tooLong || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("Names have at most %d characters", MaxForwardName)
	}
	return jsstring.Trim(name), nil
}

// ForwardView is a forward as shown (ForwardSchema): its fields and addresses, as JSON.
func ForwardView(forward Forward, radiusOrigin *string, sessionTitle string) *chordjson.Object {
	view := chordjson.ObjectOf("id", forward.ID, "port", float64(forward.Port))
	if forward.Name != "" {
		view.Set("name", forward.Name)
	}
	if forward.Public {
		view.Set("public", true)
	}
	if forward.Session != "" {
		view.Set("session", forward.Session)
	}
	if sessionTitle != "" {
		view.Set("sessionTitle", sessionTitle)
	}
	if radiusOrigin != nil && *radiusOrigin != "" {
		if url, ok := ForwardOrigin(*radiusOrigin, forward.ID, false); ok {
			view.Set("url", url+"/")
		}
		if forward.Public {
			if url, ok := ForwardOrigin(*radiusOrigin, forward.ID, true); ok {
				view.Set("publicUrl", url+"/")
			}
		}
	}
	return view
}

// Forwards are the port forwards, kept in `forwards.json` of the data directory as
// `{ "<id>": { "port", "name"?, "public"?, "session"? } }`.
//
// The owner manages them in the Ports sheet; a session's agent opens and closes them through the `ports` tool (`session`
// names the session that opened one). The agent may change or close any forward; a session's forwards close when it is
// archived.
//
// The server is the only reader and writer: the Radius relay client looks a forward up for every relayed request, so the list
// lives in memory and every change is written through. A forward's ID is random and becomes part of its URL; removing a
// forward and adding the same port again gives a new URL, so a removed forward's links stop working.
//
// A Forwards is safe for concurrent use: the relay looks forwards up off the host's loop.
type Forwards struct {
	path string

	mu      sync.Mutex
	order   []string
	byID    map[string]Forward
	origin  *string
	next    int
	listens map[int]func()
	// saving serializes the writes, each with the whole list as it is then.
	saving sync.Mutex
}

// OpenForwards reads the forwards of directory (none without `forwards.json`).
func OpenForwards(directory string) (*Forwards, error) {
	path := filepath.Join(directory, "forwards.json")
	forwards := &Forwards{path: path, byID: map[string]Forward{}, listens: map[int]func(){}}
	text, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return forwards, nil
	}
	if err != nil {
		return nil, err
	}
	parsed, err := chordjson.Decode(text)
	if err != nil {
		return nil, err
	}
	object, ok := parsed.(*chordjson.Object)
	if !ok || object == nil {
		return nil, errors.New("Invalid forwards.json")
	}
	for id, entry := range object.All() {
		if !IsForwardID(id) {
			return nil, errors.New("Invalid forwards.json")
		}
		var fields *chordjson.Object
		switch typed := entry.(type) {
		case *chordjson.Object:
			fields = typed
		case []any:
			// An array is an entry without fields: its port is what fails.
			fields = chordjson.NewObject(0)
		default:
			return nil, errors.New("Invalid forwards.json")
		}
		forward := Forward{ID: id}
		if name, present := fields.Get("name"); present {
			text, isText := name.(string)
			if !isText {
				return nil, fmt.Errorf("Names have at most %d characters", MaxForwardName)
			}
			if forward.Name, err = validName(text); err != nil {
				return nil, err
			}
		}
		if public, present := fields.Get("public"); present {
			flag, isFlag := public.(bool)
			if !isFlag {
				return nil, errors.New("Invalid forwards.json")
			}
			forward.Public = flag
		}
		if session, present := fields.Get("session"); present {
			text, isText := session.(string)
			if !isText || text == "" {
				return nil, errors.New("Invalid forwards.json")
			}
			forward.Session = text
		}
		if forward.Port, err = ValidateForwardPort(fields.Value("port")); err != nil {
			return nil, err
		}
		forwards.set(forward)
	}
	return forwards, nil
}

// set stores forward, keeping the place of an earlier one with its ID.
func (f *Forwards) set(forward Forward) {
	if _, known := f.byID[forward.ID]; !known {
		f.order = append(f.order, forward.ID)
	}
	f.byID[forward.ID] = forward
}

func (f *Forwards) delete(id string) bool {
	if _, known := f.byID[id]; !known {
		return false
	}
	delete(f.byID, id)
	f.order = slices.DeleteFunc(f.order, func(each string) bool { return each == id })
	return true
}

// Get is the forward with id.
func (f *Forwards) Get(id string) (Forward, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	forward, ok := f.byID[id]
	return forward, ok
}

// List is the forwards by port, then ID.
func (f *Forwards) List() []Forward {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := make([]Forward, 0, len(f.order))
	for _, id := range f.order {
		list = append(list, f.byID[id])
	}
	slices.SortFunc(list, func(a, b Forward) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return strings.Compare(a.ID, b.ID)
	})
	return list
}

// RadiusOrigin is the brain's Radius origin: saved in radius.json, updated from relayed requests for its UI; nil while
// unknown.
func (f *Forwards) RadiusOrigin() *string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.origin
}

// SetRadiusOrigin sets the Radius origin and tells the listeners when it changed.
func (f *Forwards) SetRadiusOrigin(origin *string) {
	f.mu.Lock()
	same := f.origin == origin || f.origin != nil && origin != nil && *f.origin == *origin
	if !same {
		f.origin = origin
	}
	f.mu.Unlock()
	if !same {
		f.notify()
	}
}

// Subscribe calls listener at every change from now on (a change of the list, as written, or of the origin); the result
// stops it. A listener may be called from any goroutine.
func (f *Forwards) Subscribe(listener func()) (stop func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.next
	f.next++
	f.listens[id] = listener
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.listens, id)
	}
}

func (f *Forwards) notify() {
	f.mu.Lock()
	listeners := make([]func(), 0, len(f.listens))
	for _, id := range slices.Sorted(maps.Keys(f.listens)) {
		listeners = append(listeners, f.listens[id])
	}
	f.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

// Add forwards port (reserved: ports that must never be forwarded, such as this server's own). session is "" for the owner's.
func (f *Forwards) Add(port int, name *string, reserved []int, public bool, session string) (Forward, error) {
	forward, err := f.add(port, name, reserved, public, session)
	if err != nil {
		return Forward{}, err
	}
	return forward, f.save()
}

func (f *Forwards) add(port int, name *string, reserved []int, public bool, session string) (Forward, error) {
	if err := validPort(port); err != nil {
		return Forward{}, err
	}
	if slices.Contains(reserved, port) {
		return Forward{}, errors.New("This is Pi's own port; it is already reachable through Radius")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.byID) >= maxForwards {
		return Forward{}, fmt.Errorf("At most %d forwards", maxForwards)
	}
	valid, err := ValidateForwardName(name)
	if err != nil {
		return Forward{}, err
	}
	forward := Forward{ID: f.newID(), Port: port, Name: valid, Public: public, Session: session}
	f.set(forward)
	return forward, nil
}

// newID is a random ID not in use; f.mu is held.
func (f *Forwards) newID() string {
	for {
		bytes := make([]byte, 10)
		if _, err := rand.Read(bytes); err != nil {
			panic(err)
		}
		id := make([]byte, len(bytes))
		for at, each := range bytes {
			id[at] = idAlphabet[int(each)%len(idAlphabet)]
		}
		if _, used := f.byID[string(id)]; !used {
			return string(id)
		}
	}
}

// OpenOptions are what the `ports` tool's open sets: absent fields keep the forward's.
type OpenOptions struct {
	Name   *string
	Public *bool
}

// OpenForSession is the `ports` tool's open: one forward per port. A port that is already forwarded keeps its address and its
// opener, and takes the new name and visibility, whoever opened it.
func (f *Forwards) OpenForSession(session string, port int, options OpenOptions, reserved []int) (Forward, error) {
	if err := validPort(port); err != nil {
		return Forward{}, err
	}
	f.mu.Lock()
	existing, found := f.findPort(port)
	f.mu.Unlock()
	if !found {
		return f.Add(port, options.Name, reserved, options.Public != nil && *options.Public, session)
	}
	name := existing.Name
	if options.Name != nil {
		var err error
		if name, err = validName(*options.Name); err != nil {
			return Forward{}, err
		}
	}
	open := existing.Public
	if options.Public != nil {
		open = *options.Public
	}
	updated := Forward{ID: existing.ID, Port: port, Name: name, Public: open, Session: existing.Session}
	f.mu.Lock()
	f.set(updated)
	f.mu.Unlock()
	return updated, f.save()
}

// findPort is the first forward of port, in the order they were added; f.mu is held.
func (f *Forwards) findPort(port int) (Forward, bool) {
	for _, id := range f.order {
		if forward := f.byID[id]; forward.Port == port {
			return forward, true
		}
	}
	return Forward{}, false
}

// ClosePort is the `ports` tool's close, whoever opened the forward; false when the port was not forwarded.
func (f *Forwards) ClosePort(port int) (bool, error) {
	f.mu.Lock()
	existing, found := f.findPort(port)
	if found {
		f.delete(existing.ID)
	}
	f.mu.Unlock()
	if !found {
		return false, nil
	}
	return true, f.save()
}

// CloseSession closes every forward a session opened, as when it is archived.
func (f *Forwards) CloseSession(session string) error {
	f.mu.Lock()
	changed := false
	for _, id := range slices.Clone(f.order) {
		if f.byID[id].Session == session {
			changed = f.delete(id) || changed
		}
	}
	f.mu.Unlock()
	if !changed {
		return nil
	}
	return f.save()
}

// SetPublic opens a forward to anyone with the link, or only to the owner.
func (f *Forwards) SetPublic(id string, open bool) error {
	f.mu.Lock()
	forward, found := f.byID[id]
	if !found {
		f.mu.Unlock()
		return errors.New("Unknown forward")
	}
	forward.Public = open
	f.byID[id] = forward
	f.mu.Unlock()
	return f.save()
}

// Remove forgets a forward.
func (f *Forwards) Remove(id string) error {
	f.mu.Lock()
	removed := f.delete(id)
	f.mu.Unlock()
	if !removed {
		return errors.New("Unknown forward")
	}
	return f.save()
}

// save writes the whole list as it is when the write runs (writes run one at a time), then tells the listeners.
func (f *Forwards) save() error {
	if err := f.write(); err != nil {
		return err
	}
	f.notify()
	return nil
}

func (f *Forwards) write() error {
	f.saving.Lock()
	defer f.saving.Unlock()
	f.mu.Lock()
	value := chordjson.NewObject(len(f.order))
	for _, id := range f.order {
		forward := f.byID[id]
		fields := chordjson.ObjectOf("port", float64(forward.Port))
		if forward.Name != "" {
			fields.Set("name", forward.Name)
		}
		if forward.Public {
			fields.Set("public", true)
		}
		if forward.Session != "" {
			fields.Set("session", forward.Session)
		}
		value.Set(id, fields)
	}
	f.mu.Unlock()
	compact, err := chordjson.Marshal(value)
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "\t"); err != nil {
		return err
	}
	out.WriteByte('\n')
	if err := os.WriteFile(f.path+".tmp", out.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(f.path+".tmp", f.path)
}
