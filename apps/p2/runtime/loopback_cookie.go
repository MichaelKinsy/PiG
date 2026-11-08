package runtime

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Ports apps/p2/src/runtime/loopback-cookie.ts: the loopback owner's browser cookie (Pi Mobile's loopback owner cookie):
// set when the owner's browser loads the app on loopback. Loopback requests without any cookie are still the owner
// (`access: "local"`: the TUI, owner-link, the app's first page load), but only a browser carrying this cookie is the
// owner for the share join page, and it takes precedence over a guest cookie in the same browser. Meaningless off
// loopback: checked only there. The cookie and its secret file keep their names (`p2_device_<port>`, `device-cookie`),
// so browsers that have it keep it.

var (
	cookieSecretsMu sync.Mutex
	cookieSecrets   = map[string]string{}
	secretFormat    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// secretOf is the data directory's cookie value (`device-cookie`, created once, private).
func secretOf(dataDir string) (string, error) {
	cookieSecretsMu.Lock()
	defer cookieSecretsMu.Unlock()
	if secret, ok := cookieSecrets[dataDir]; ok {
		return secret, nil
	}
	path := filepath.Join(dataDir, "device-cookie")
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		random := make([]byte, 32)
		if _, randErr := rand.Read(random); randErr != nil {
			return "", randErr
		}
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		switch {
		case openErr == nil:
			_, writeErr := file.WriteString(hex.EncodeToString(random))
			if closeErr := file.Close(); writeErr == nil {
				writeErr = closeErr
			}
			if writeErr != nil {
				return "", writeErr
			}
		case !errors.Is(openErr, os.ErrExist):
			return "", openErr
		}
		content, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	secret := strings.TrimSpace(string(content))
	if !secretFormat.MatchString(secret) {
		return "", fmt.Errorf("Invalid %s", path)
	}
	cookieSecrets[dataDir] = secret
	return secret, nil
}

// LoopbackCookieName is the cookie's name on port.
func LoopbackCookieName(port int) string { return "p2_device_" + strconv.Itoa(port) }

// LoopbackCookie is the `Set-Cookie` value for the owner's browser on loopback.
func LoopbackCookie(port int, dataDir string) (string, error) {
	secret, err := secretOf(dataDir)
	if err != nil {
		return "", err
	}
	return LoopbackCookieName(port) + "=" + secret + "; HttpOnly; SameSite=Strict; Path=/; Max-Age=2592000", nil
}

// HasLoopbackCookie reports whether the request carries the loopback owner cookie (the caller checks that it came over
// loopback).
func HasLoopbackCookie(request *http.Request, port int, dataDir string) (bool, error) {
	value, ok := CookieValue(request, LoopbackCookieName(port))
	if !ok {
		return false, nil
	}
	secret, err := secretOf(dataDir)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(secret), []byte(value)) == 1, nil
}
