package plugins

import "errors"

type sourceMapEntry struct {
	originalSource string
	hasSource      bool
	originalLine   int
	originalColumn int
}

type sourceMap struct{}

func parseSourceMap(text []byte) (*sourceMap, error) { return nil, errors.New("not implemented") }

func inlineSourceMap(source string) (*sourceMap, error) { return nil, errors.New("not implemented") }

func (m *sourceMap) findEntry(line, column int) (sourceMapEntry, bool) {
	return sourceMapEntry{}, false
}
