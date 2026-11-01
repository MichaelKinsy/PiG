package packagecontent

import (
	"errors"

	"github.com/MichaelKinsy/PiG/internal/minimatch"
)

// newMinimatchFilter is new Minimatch(pattern) of Pi's minimatch 11.0.3 with its default options, as Pi's matchesAnyPattern builds one for each minimatch(path, pattern) call (package-manager.ts:661-685): case is not ignored, "#" starts a comment, leading "!"s negate, and the parts go through the first optimization level only. It fails where the constructor throws: for a pattern over MAX_PATTERN_LENGTH.
func newMinimatchFilter(pattern string) (*minimatch.Minimatch, error) {
	m, err := minimatch.New(pattern, minimatch.Options{})
	if errors.Is(err, minimatch.ErrPatternTooLong) {
		return nil, errGlobPatternTooLong
	}
	return m, err
}

// filterPatterns are the patterns of one matchesAnyPattern call, each in Pi's toPosixPath(normalizePath(pattern)) form. Pi builds a Minimatch for each minimatch(path, pattern) call, so a pattern is normalized and compiled when a path first reaches it: one after a pattern that already matched is never compiled, and one that cannot be normalized (an invalid file URL) or compiled fails only when a path reaches it.
type filterPatterns struct {
	patterns []string
	invalid  []error
	compiled []*minimatch.Minimatch
}

func newFilterPatterns(patterns []string) *filterPatterns {
	normalized := make([]string, len(patterns))
	invalid := make([]error, len(patterns))
	for i, pattern := range patterns {
		// upstream: package-manager.ts:matchesAnyPattern (toPosixPath(normalizePath(pattern)), Pi #10765)
		normalized[i], invalid[i] = normalizePatternPath(pattern)
	}
	return &filterPatterns{patterns: normalized, invalid: invalid, compiled: make([]*minimatch.Minimatch, len(patterns))}
}

func (f *filterPatterns) at(i int) (*minimatch.Minimatch, error) {
	if f.invalid[i] != nil {
		return nil, f.invalid[i]
	}
	if f.compiled[i] == nil {
		m, err := newMinimatchFilter(f.patterns[i])
		if err != nil {
			return nil, err
		}
		f.compiled[i] = m
	}
	return f.compiled[i], nil
}
