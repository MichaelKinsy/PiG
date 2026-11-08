package desktop

import (
	"net/url"
	"regexp"
	"strings"
)

// Scheme is the URL scheme the app registers.
const Scheme = "pig"

var (
	sessionID    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	conversation = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
)

// RouteOf maps a deep link to the path of the p2 page it opens, and reports whether the link is one the app knows:
//
//	pig://open                       the home page (also brings the window forward)
//	pig://session/<id>               /s/<id>
//	pig://session/<id>/c/<n>         /s/<id>/c/<n>
//
// A deep link is input from any program or web page on the machine, so it is never turned into a URL of another origin or
// a path outside these shapes: the id and the conversation number are matched against the shapes p2's own router accepts
// (`pathRoute`: `/s/<session>` with an optional `/c/<n>`), and everything else is refused.
// pig additive (D481): pig:// links open only the page shapes p2's router knows.
func RouteOf(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, Scheme) || u.User != nil || u.Port() != "" {
		return "", false
	}
	switch strings.ToLower(u.Host) {
	case "open":
		if u.Path == "" || u.Path == "/" {
			return "/", true
		}
	case "session":
		parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
		switch {
		case len(parts) == 1 && sessionID.MatchString(parts[0]):
			return "/s/" + parts[0], true
		case len(parts) == 3 && sessionID.MatchString(parts[0]) && parts[1] == "c" && conversation.MatchString(parts[2]):
			return "/s/" + parts[0] + "/c/" + parts[2], true
		}
	}
	return "", false
}

// sessionRoute is the path of a session's page.
func sessionRoute(session string) (string, bool) {
	if !sessionID.MatchString(session) {
		return "", false
	}
	return "/s/" + session, true
}

// sessionOfRoute is the session a route opens ("": none).
func sessionOfRoute(route string) (string, bool) {
	rest, ok := strings.CutPrefix(route, "/s/")
	if !ok {
		return "", false
	}
	id, _, _ := strings.Cut(rest, "/")
	return id, sessionID.MatchString(id)
}
