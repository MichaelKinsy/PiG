package host

type listener struct {
	subs map[string]func(string)
}

// chord/host is in scope.
func (l *listener) bad() {
	for key, fn := range l.subs { // want `range over a map invokes the callback fn`
		fn(key)
	}
}
