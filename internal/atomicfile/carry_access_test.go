package atomicfile

import (
	"errors"
	"slices"
	"testing"
)

// fakeFile is a file whose access the calls of a carry change, as the Win32 calls change it; calls records the order they were made in.
type fakeFile struct {
	state   access
	calls   []string
	readErr error
}

func (f *fakeFile) call(name string, effect func(*access), err error) func() error {
	return func() error {
		f.calls = append(f.calls, name)
		effect(&f.state)
		return err
	}
}

func (f *fakeFile) read() (access, error) {
	f.calls = append(f.calls, "read")
	return f.state, f.readErr
}

var (
	wantAI     = access{protected: true, autoInherited: true, entries: []string{"owner"}}
	wantNoFlag = access{protected: true, entries: []string{"owner"}}
	staleFile  = access{entries: []string{"stale"}}
	// the effects of the three calls as the Windows job showed them: exact sets the access and clears the flag, named sets the flag too.
	setExact = func(want access) func(*access) {
		return func(a *access) { *a = access{protected: want.protected, entries: want.entries} }
	}
	setFlag    = func(a *access) { a.autoInherited = true }
	changeAces = func(a *access) { a.entries = []string{"inheritance rewrote the entries"}; a.autoInherited = true }
	nothing    = func(*access) {}
)

// calls without the read-backs, which every set call is followed by.
func setCalls(f *fakeFile) []string {
	var set []string
	for _, c := range f.calls {
		if c != "read" {
			set = append(set, c)
		}
	}
	return set
}

func TestCarryAccessChoosesTheCallsByWhatTheReadBackShows(t *testing.T) {
	boom := errors.New("the call failed")
	for _, tc := range []struct {
		name         string
		want, file   access
		exact        func(*access)
		asked, named func(*access) // nil: the call is not offered (named is nil for the live file)
		askedErr     error
		wantCalls    []string
		wantVerdict  error // nil, errAutoInheritedDiffers or errAccessDiffers
		wantErrs     error
	}{
		{name: "a file that already has the access and the flag is not touched", want: wantAI, file: wantAI, exact: setExact(wantAI), asked: setFlag, named: setFlag,
			wantCalls: nil},
		{name: "a file without the flag where none is wanted is not touched", want: wantNoFlag, file: wantNoFlag, exact: setExact(wantNoFlag), asked: setFlag, named: setFlag,
			wantCalls: nil},
		{name: "without the flag wanted, the flag the file has is cleared by exact alone", want: wantNoFlag, file: access{protected: true, autoInherited: true, entries: []string{"owner"}},
			exact: setExact(wantNoFlag), asked: setFlag, named: setFlag, wantCalls: []string{"exact"}},
		{name: "other access and no flag wanted: exact alone", want: wantNoFlag, file: staleFile, exact: setExact(wantNoFlag), asked: setFlag, named: setFlag,
			wantCalls: []string{"exact"}},
		{name: "the access is already right and only the flag is missing: exact is not called and the flag does not go", want: wantAI, file: wantNoFlag,
			exact: setExact(wantAI), asked: setFlag, named: setFlag, wantCalls: []string{"asked"}},
		{name: "exact clears the flag and asked gives it back: named is not called", want: wantAI, file: staleFile, exact: setExact(wantAI), asked: setFlag, named: setFlag,
			wantCalls: []string{"exact", "asked"}},
		{name: "asked leaves the flag off: named gives it", want: wantAI, file: staleFile, exact: setExact(wantAI), asked: nothing, named: setFlag,
			wantCalls: []string{"exact", "asked", "named"}},
		{name: "asked changes the entries: exact undoes it, then named gives the flag", want: wantAI, file: staleFile, exact: setExact(wantAI), asked: changeAces, named: setFlag,
			wantCalls: []string{"exact", "asked", "exact", "named"}},
		{name: "named changes the entries: exact undoes it and the write succeeds without the flag", want: wantAI, file: staleFile, exact: setExact(wantAI), asked: nothing, named: changeAces,
			wantCalls: []string{"exact", "asked", "named", "exact"}, wantVerdict: errAutoInheritedDiffers},
		{name: "neither call sets the flag: the write succeeds without it", want: wantAI, file: staleFile, exact: setExact(wantAI), asked: nothing, named: nothing,
			wantCalls: []string{"exact", "asked", "named"}, wantVerdict: errAutoInheritedDiffers},
		{name: "the live file is never offered named", want: wantAI, file: staleFile, exact: setExact(wantAI), asked: nothing, named: nil,
			wantCalls: []string{"exact", "asked"}, wantVerdict: errAutoInheritedDiffers},
		{name: "exact leaves other access: no other call is made, the access differs", want: wantAI, file: staleFile, exact: nothing, asked: setFlag, named: setFlag,
			wantCalls: []string{"exact"}, wantVerdict: errAccessDiffers},
		{name: "exact cannot undo what asked did: the access differs", want: wantAI, file: staleFile, exact: func(a *access) {
			if a.entries[0] == "stale" {
				*a = access{protected: true, entries: []string{"owner"}}
			}
		}, asked: changeAces, named: setFlag, wantCalls: []string{"exact", "asked", "exact"}, wantVerdict: errAccessDiffers},
		{name: "a call that fails but did its work counts by the read-back; its error is kept", want: wantAI, file: staleFile, exact: setExact(wantAI),
			asked: setFlag, askedErr: boom, wantCalls: []string{"exact", "asked"}, wantErrs: boom},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeFile{state: tc.file}
			calls := carryCalls{exact: f.call("exact", tc.exact, nil), read: f.read}
			if tc.asked != nil {
				calls.asked = f.call("asked", tc.asked, tc.askedErr)
			}
			if tc.named != nil {
				calls.named = f.call("named", tc.named, nil)
			}
			got, callErrs, err := carryAccess(tc.want, calls)
			if err != nil {
				t.Fatalf("carryAccess error = %v", err)
			}
			if verdict := carryVerdict(tc.want, got); !errors.Is(verdict, tc.wantVerdict) || (verdict == nil) != (tc.wantVerdict == nil) {
				t.Errorf("verdict = %v, want %v (read back %+v)", verdict, tc.wantVerdict, got)
			}
			if set := setCalls(f); !slices.Equal(set, tc.wantCalls) {
				t.Errorf("calls = %v, want %v", set, tc.wantCalls)
			}
			if !errors.Is(callErrs, tc.wantErrs) || (callErrs == nil) != (tc.wantErrs == nil) {
				t.Errorf("call errors = %v, want %v", callErrs, tc.wantErrs)
			}
		})
	}
}

// A read-back that fails is the error of the carry, wherever it is made: nothing is known of the file then.
func TestCarryAccessReturnsAFailedReadBack(t *testing.T) {
	failure := errors.New("cannot read the file's descriptor")
	f := &fakeFile{state: staleFile, readErr: failure}
	calls := carryCalls{exact: f.call("exact", setExact(wantAI), nil), asked: f.call("asked", setFlag, nil), named: f.call("named", setFlag, nil), read: f.read}
	if _, _, err := carryAccess(wantAI, calls); !errors.Is(err, failure) {
		t.Fatalf("carryAccess error = %v, want the failed read", err)
	}
	if set := setCalls(f); len(set) != 0 {
		t.Fatalf("calls = %v, want none: the file is read before anything is set", set)
	}
}
