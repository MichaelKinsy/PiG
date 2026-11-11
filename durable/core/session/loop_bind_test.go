package session_test

import "github.com/MichaelKinsy/PiG/durable/core/session"

// bound is a Loop over s: s's commits are its line jobs.
func bound(s *session.Session) *session.Loop {
	l := &session.Loop{}
	l.Bind(s)
	return l
}
