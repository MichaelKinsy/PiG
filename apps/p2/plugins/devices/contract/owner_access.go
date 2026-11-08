package contract

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Ports apps/p2/plugins/devices/owner-access.ts.

const (
	tokenLifetime  = 10 * 60_000
	cookieLifetime = 30 * 24 * 60 * 60_000
	maxInvitations = 32
	maxSafeInteger = 1<<53 - 1
)

var (
	keyPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	devicePattern = regexp.MustCompile(`^([a-f0-9]{32})\.([0-9]+)\.([a-f0-9]{64})$`)
	idPattern     = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

// OwnerCookieName is the name of the owner cookie of a server on port.
func OwnerCookieName(port int) string { return "p2_owner_" + strconv.Itoa(port) }

func hashOf(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	buffer := make([]byte, n)
	// crypto/rand does not fail on the supported systems (it terminates the program if it would).
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

// privateWrite atomically replaces private state; a failed write never truncates the previous key or revocations.
func privateWrite(path, content string) error {
	temporary := path + "." + randomHex(8) + ".tmp"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// jsTrim is `String.prototype.trim`: whitespace and line terminators, including U+FEFF.
func jsTrim(text string) string {
	return strings.TrimFunc(text, func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
			return true
		}
		return r >= 0x2000 && r <= 0x200A
	})
}

// Device is one signed browser cookie: its ID and its expiry (ms).
type Device struct {
	ID      string
	Expires int64
}

type invitation struct {
	id      string
	expires int64
}

type revocation struct {
	id      string
	expires int64
}

// OwnerAccess is the installation-owned key and one-time tokens. Browser cookies are distinct signed devices, not the
// installation key. Invitations stay single-use in memory.
type OwnerAccess struct {
	directory string
	now       func() int64

	// operations serializes revoke/rotate (writes); mu guards the fields.
	operations sync.Mutex
	mu         sync.Mutex
	key        string
	invites    []invitation
	revoked    []revocation
}

// NowMillis is the wall clock in ms.
func NowMillis() int64 { return time.Now().UnixMilli() }

// OpenOwnerAccess opens the owner key in directory (creating it, private), rotating a pre-signed-cookie key once, reading
// the revocations, and writing a fresh headless invitation to `owner-token.txt`. A nil now is the wall clock.
func OpenOwnerAccess(directory string, now func() int64) (*OwnerAccess, error) {
	if now == nil {
		now = NowMillis
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "owner-key")
	var key string
	content, err := os.ReadFile(path)
	switch {
	case err == nil:
		key = jsTrim(string(content))
	case errors.Is(err, fs.ErrNotExist):
		key = randomHex(32)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		_, err = file.WriteString(key)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	if !keyPattern.MatchString(key) {
		return nil, errors.New("Invalid owner key")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	// The previous cookie WAS the owner key. Rotate it once before using a private signing key, otherwise an old device
	// could manufacture signed cookies after the format change.
	schemePath := filepath.Join(directory, "owner-cookie-version")
	scheme, err := os.ReadFile(schemePath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		key = randomHex(32)
		if err := privateWrite(path, key); err != nil {
			return nil, err
		}
		if err := privateWrite(schemePath, "signed-v1\n"); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case jsTrim(string(scheme)) != "signed-v1":
		return nil, errors.New("Invalid owner cookie version")
	}
	revoked, err := readRevocations(filepath.Join(directory, "owner-revoked.json"))
	if err != nil {
		return nil, err
	}
	access := &OwnerAccess{directory: directory, key: key, revoked: revoked, now: now}
	// Every startup has a fresh headless invitation. Never log it or put it in a query/path.
	if err := privateWrite(filepath.Join(directory, "owner-token.txt"), access.IssueToken()+"\n"); err != nil {
		return nil, err
	}
	return access, nil
}

func readRevocations(path string) ([]revocation, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	invalid := fmt.Errorf("Cannot read owner revocations")
	if err != nil {
		return nil, invalid
	}
	var value any
	if err := json.Unmarshal(content, &value); err != nil {
		return nil, invalid
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, invalid
	}
	// JSON.parse keeps the order of keys; encoding/json does not, so the order is read from the text.
	order, err := objectKeys(content)
	if err != nil {
		return nil, invalid
	}
	var out []revocation
	for _, id := range order {
		expires, isNumber := object[id].(float64)
		if !idPattern.MatchString(id) || !isNumber || expires != math.Trunc(expires) || math.Abs(expires) > maxSafeInteger {
			return nil, invalid
		}
		out = append(out, revocation{id: id, expires: int64(expires)})
	}
	return out, nil
}

// objectKeys is the distinct keys of the top-level JSON object in text, in first-seen order.
func objectKeys(text []byte) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var keys []string
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := token.(string)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// IssueToken is a fresh single-use invitation, valid for ten minutes. At most 32 are outstanding: the oldest goes.
func (a *OwnerAccess) IssueToken() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.issueLocked()
}

func (a *OwnerAccess) issueLocked() string {
	now := a.now()
	kept := a.invites[:0]
	for _, each := range a.invites {
		if each.expires > now {
			kept = append(kept, each)
		}
	}
	a.invites = kept
	if len(a.invites) >= maxInvitations {
		a.invites = a.invites[1:]
	}
	token := randomHex(32)
	a.invites = append(a.invites, invitation{id: hashOf(token), expires: now + tokenLifetime})
	return token
}

// Claim consumes an invitation (whitespace around it is ignored): true when it was outstanding and unexpired.
func (a *OwnerAccess) Claim(token string) bool {
	id := hashOf(jsTrim(token))
	a.mu.Lock()
	defer a.mu.Unlock()
	for index, each := range a.invites {
		if each.id == id {
			a.invites = append(a.invites[:index:index], a.invites[index+1:]...)
			return each.expires > a.now()
		}
	}
	return false
}

func (a *OwnerAccess) signature(key string, port int, payload string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(strconv.Itoa(port) + ":" + payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// Cookie is the `Set-Cookie` value of a new signed device on port.
func (a *OwnerAccess) Cookie(port int, secure bool) string {
	a.mu.Lock()
	key := a.key
	a.mu.Unlock()
	payload := randomHex(16) + "." + strconv.FormatInt(a.now()+cookieLifetime, 10)
	suffix := ""
	if secure {
		suffix = "; Secure"
	}
	return OwnerCookieName(port) + "=" + payload + "." + a.signature(key, port, payload) + "; HttpOnly; SameSite=Lax; Path=/; Max-Age=2592000" + suffix
}

// ClearCookie is the `Set-Cookie` value that clears the owner cookie.
func (a *OwnerAccess) ClearCookie(port int, secure bool) string {
	suffix := ""
	if secure {
		suffix = "; Secure"
	}
	return OwnerCookieName(port) + "=; HttpOnly" + suffix + "; SameSite=Lax; Path=/; Max-Age=0"
}

// Device is the signed, unexpired, unrevoked device the request's cookie names (nil: none).
func (a *OwnerAccess) Device(request *http.Request, port int) *Device {
	value, ok := p2runtime.CookieValue(request, OwnerCookieName(port))
	if !ok {
		return nil
	}
	match := devicePattern.FindStringSubmatch(value)
	if match == nil {
		return nil
	}
	id, expiry, signature := match[1], match[2], match[3]
	parsed, err := strconv.ParseFloat(expiry, 64)
	if err != nil || parsed > maxSafeInteger {
		return nil
	}
	expires := int64(parsed)
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if expires <= now {
		return nil
	}
	for _, each := range a.revoked {
		if each.id == id && each.expires > now {
			return nil
		}
	}
	if !hmac.Equal([]byte(signature), []byte(a.signature(a.key, port, id+"."+expiry))) {
		return nil
	}
	return &Device{ID: id, Expires: expires}
}

// Authorized reports whether the request carries a valid owner cookie.
func (a *OwnerAccess) Authorized(request *http.Request, port int) bool {
	return a.Device(request, port) != nil
}

func (a *OwnerAccess) writeRevoked() error {
	var text bytes.Buffer
	text.WriteByte('{')
	for index, each := range a.revoked {
		if index > 0 {
			text.WriteByte(',')
		}
		fmt.Fprintf(&text, "%q:%d", each.id, each.expires)
	}
	text.WriteByte('}')
	return privateWrite(filepath.Join(a.directory, "owner-revoked.json"), text.String())
}

// Logout revokes one device, persistently.
func (a *OwnerAccess) Logout(device Device) error {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.mu.Lock()
	now := a.now()
	kept := make([]revocation, 0, len(a.revoked)+1)
	for _, each := range a.revoked {
		if each.expires > now && each.id != device.ID {
			kept = append(kept, each)
		}
	}
	a.revoked = append(kept, revocation{id: device.ID, expires: device.Expires})
	err := a.writeRevoked()
	a.mu.Unlock()
	return err
}

// Rotate replaces the signing key: every cookie and invitation ends, and a fresh headless invitation is written.
func (a *OwnerAccess) Rotate() error {
	a.operations.Lock()
	defer a.operations.Unlock()
	next := randomHex(32)
	if err := privateWrite(filepath.Join(a.directory, "owner-key"), next); err != nil {
		return err
	}
	a.mu.Lock()
	a.key = next
	a.invites = nil
	a.revoked = nil
	err := a.writeRevoked()
	a.mu.Unlock()
	if err != nil {
		return err
	}
	return privateWrite(filepath.Join(a.directory, "owner-token.txt"), a.IssueToken()+"\n")
}

// Close forgets the outstanding invitations once pending writes finished.
func (a *OwnerAccess) Close() {
	a.operations.Lock()
	defer a.operations.Unlock()
	a.mu.Lock()
	a.invites = nil
	a.mu.Unlock()
}

// Directory is where the access keeps its key and revocations.
func (a *OwnerAccess) Directory() string { return a.directory }
