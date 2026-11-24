//go:build windows

package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A write through WriteFile must leave the whole security descriptor of the file as it was: the owner, the group, the DACL's entries with their flags,
// SE_DACL_PROTECTED and SE_DACL_AUTO_INHERITED. Pi writes the file in place, so its descriptor never changes. These tests cross every starting descriptor
// that a credential, settings or MCP file has on a Windows machine with every route a replacement takes, record the descriptor before, after the first
// write and after a second write, and when a cell differs print every value of that cell, the steps of the route in between, and the same carry tried
// through the other Win32 calls on fresh files.

// seen is a file's security descriptor as the matrix compares it.
type seen struct {
	sddl          string // owner, group and DACL, as Windows prints them
	owner, group  string
	aces          string // the DACL's entries in SDDL, each with its flags; "" for a DACL without entries
	protected     bool   // SE_DACL_PROTECTED
	autoInherited bool   // SE_DACL_AUTO_INHERITED
}

func snapshot(path string) (seen, error) {
	const parts = windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION
	full, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, parts)
	if err != nil {
		return seen{}, err
	}
	dacl, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return seen{}, err
	}
	if full == nil || dacl == nil {
		return seen{}, errors.New("the file has no security descriptor")
	}
	control, _, err := full.Control()
	if err != nil {
		return seen{}, err
	}
	state := seen{
		sddl:          full.String(),
		protected:     control&windows.SE_DACL_PROTECTED != 0,
		autoInherited: control&windows.SE_DACL_AUTO_INHERITED != 0,
	}
	if owner, _, err := full.Owner(); err == nil && owner != nil {
		state.owner = owner.String()
	}
	if group, _, err := full.Group(); err == nil && group != nil {
		state.group = group.String()
	}
	if _, entries, found := strings.Cut(dacl.String(), "("); found {
		state.aces = "(" + entries
	}
	return state, nil
}

func mustSnapshot(t testing.TB, path string) seen {
	t.Helper()
	state, err := snapshot(path)
	if err != nil {
		t.Fatalf("security descriptor of %s: %v", path, err)
	}
	return state
}

// describe is the descriptor of path for a log line.
func describe(path string) string {
	state, err := snapshot(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return state.sddl
}

// changed names the parts of the descriptor that differ.
func changed(before, after seen) string {
	var parts []string
	for _, part := range []struct {
		name string
		same bool
	}{
		{"owner", before.owner == after.owner},
		{"group", before.group == after.group},
		{"entries", before.aces == after.aces},
		{"SE_DACL_PROTECTED", before.protected == after.protected},
		{"SE_DACL_AUTO_INHERITED", before.autoInherited == after.autoInherited},
	} {
		if !part.same {
			parts = append(parts, part.name)
		}
	}
	return strings.Join(parts, ", ")
}

// observation is the state of one file after one step of a route.
type observation struct {
	step  string
	state seen
	err   error
}

type recording struct{ observations []observation }

func (r *recording) note(step, path string) {
	state, err := snapshot(path)
	r.observations = append(r.observations, observation{step: step, state: state, err: err})
}

func (r *recording) String() string {
	var b strings.Builder
	for _, o := range r.observations {
		if o.err != nil {
			fmt.Fprintf(&b, "\n    %-44s <%v>", o.step, o.err)
			continue
		}
		fmt.Fprintf(&b, "\n    %-44s %s", o.step, o.state.sddl)
	}
	return b.String()
}

func currentUserSID(t testing.TB) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
}

// createWithDescriptor creates the file at path holding "old" with the security descriptor in sddl given at creation.
func createWithDescriptor(t testing.TB, path, sddl string) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: descriptor}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, &attributes, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("create %s with %s: %v", path, sddl, err)
	}
	file := os.NewFile(uintptr(handle), path)
	if _, err := file.WriteString("old"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// createAsTheStoreDoes creates the file at path holding "old" through ownerfile.CreateNew, as AuthStorage creates its file.
func createAsTheStoreDoes(t testing.TB, path string) {
	t.Helper()
	file, err := ownerfile.CreateNew(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("old"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// setDACL gives path the DACL of sddl through SetNamedSecurityInfo, the API that propagates inheritance, protected or not.
func setDACL(t testing.TB, path, sddl string, protected bool) {
	t.Helper()
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if protected {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		info |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil); err != nil {
		t.Fatalf("set the DACL of %s to %s: %v", path, sddl, err)
	}
}

// administratorsOwn makes BUILTIN\Administrators the owner of path, or skips the test where the token cannot.
func administratorsOwn(t testing.TB, path string) {
	t.Helper()
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, administrators, nil, nil, nil); err != nil {
		t.Skipf("this token cannot make BUILTIN\\Administrators the owner of a file, so the starting descriptor of this cell cannot be made: %v", err)
	}
}

// fixture is the two directories a starting descriptor is made in: one whose files inherit the entries of the temporary directory, and one whose files
// inherit nothing.
type fixture struct {
	sid        string
	inheriting string
	plain      string
}

func newFixture(t testing.TB) fixture {
	t.Helper()
	f := fixture{sid: currentUserSID(t), inheriting: t.TempDir()}
	f.plain = filepath.Join(t.TempDir(), "plain")
	if err := os.Mkdir(f.plain, 0o700); err != nil {
		t.Fatal(err)
	}
	setDACL(t, f.plain, "D:P(A;;FA;;;"+f.sid+")", true)
	return f
}

// shape is one starting descriptor. protected and autoInherited say what the preparation aims at; the cell runs on the descriptor it got and prints both.
type shape struct {
	name                     string
	protected, autoInherited bool
	prepare                  func(t testing.TB, f fixture) string
}

func inheritedEntries(sid string) string {
	return "(A;ID;FA;;;BA)(A;ID;FA;;;SY)(A;ID;FA;;;" + sid + ")"
}

func shapes() []shape {
	inheriting := func(t testing.TB, f fixture) string {
		path := filepath.Join(f.inheriting, "file.json")
		if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	return []shape{
		{"P owner-only entry, as the store creates it", true, false, func(t testing.TB, f fixture) string {
			path := filepath.Join(f.inheriting, "file.json")
			createAsTheStoreDoes(t, path)
			return path
		}},
		{"PAI owner-only entry", true, true, func(t testing.TB, f fixture) string {
			path := filepath.Join(f.inheriting, "file.json")
			createAsTheStoreDoes(t, path)
			setDACL(t, path, "D:P(A;;FA;;;"+f.sid+")", true)
			return path
		}},
		{"P inherited-looking entries", true, false, func(t testing.TB, f fixture) string {
			path := filepath.Join(f.inheriting, "file.json")
			createWithDescriptor(t, path, "O:"+f.sid+"D:P"+inheritedEntries(f.sid))
			return path
		}},
		{"PAI Everyone-read entry (the administrator case)", true, true, func(t testing.TB, f fixture) string {
			path := inheriting(t, f)
			testenv.GrantOthersRead(t, path)
			return path
		}},
		{"AI inherited entries (os.WriteFile)", false, true, inheriting},
		{"AI explicit entry and inherited entries", false, true, func(t testing.TB, f fixture) string {
			path := inheriting(t, f)
			setDACL(t, path, "D:(A;;FA;;;"+f.sid+")", false)
			return path
		}},
		{"neither, owner-only entry, nothing inherited", false, false, func(t testing.TB, f fixture) string {
			path := filepath.Join(f.plain, "file.json")
			createWithDescriptor(t, path, "O:"+f.sid+"D:(A;;FA;;;"+f.sid+")")
			return path
		}},
		{"AI, the token's default DACL as explicit entries, nothing inherited", false, true, func(t testing.TB, f fixture) string {
			path := filepath.Join(f.plain, "file.json")
			if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}},
		{"owner Administrators, P owner-only entry", true, false, func(t testing.TB, f fixture) string {
			path := filepath.Join(f.inheriting, "file.json")
			createAsTheStoreDoes(t, path)
			administratorsOwn(t, path)
			return path
		}},
		{"owner Administrators, AI inherited entries", false, true, func(t testing.TB, f fixture) string {
			path := inheriting(t, f)
			administratorsOwn(t, path)
			return path
		}},
	}
}

// route is one way a replacement is made. asserted is false for the route that is only recorded.
type route struct {
	name     string
	asserted bool
	run      func(t testing.TB, path, data string, rec *recording) error
}

// replacementFor is what replaceThroughTemp hands to replace: a private owner-only file beside path that holds data. When carried is set the access of path
// is carried onto it before the data is written, as replaceThroughTemp does through carryAttributes; an error is a temporary file that cannot take the
// access, where WriteFile would have written in place.
func replacementFor(t testing.TB, path, data string, carried bool) (string, error) {
	t.Helper()
	file, err := createTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*", 0o600, true)
	if err != nil {
		return "", err
	}
	if carried {
		if err := carryAttributes(file, path); err != nil {
			_ = file.Close()
			return "", fmt.Errorf("the temporary file cannot take the access of the file (WriteFile writes in place then): %w", err)
		}
	}
	if _, err := file.WriteString(data); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return file.Name(), nil
}

func routes() []route {
	return []route{
		{"WriteFile", true, func(t testing.TB, path, data string, rec *recording) error {
			return WriteFile(path, []byte(data), 0o600)
		}},
		{"carry, then MoveFileEx", true, func(t testing.TB, path, data string, rec *recording) error {
			temp, err := replacementFor(t, path, data, true)
			if err != nil {
				return err
			}
			if err := carryDescriptor(path, temp); err != nil && !errors.Is(err, errAutoInheritedDiffers) {
				return err
			}
			rec.note("replacement after the second carry", temp)
			return moveFile(temp, path)
		}},
	}
}

// cellReport is one row of the table printed when a cell differs.
type cellReport struct {
	shape, route string
	before       seen
	after        [2]seen
	changes      [2]string
}

func (c cellReport) String() string {
	return fmt.Sprintf("  %s | %s\n    before            %s\n    after write 1     %s  changed: [%s]\n    after write 2     %s  changed: [%s]",
		c.shape, c.route, c.before.sddl, c.after[0].sddl, c.changes[0], c.after[1].sddl, c.changes[1])
}

// TestWindowsSecurityDescriptorMatrix runs every starting descriptor through every route of a replacement.
func TestWindowsSecurityDescriptorMatrix(t *testing.T) {
	var reports []cellReport
	cells := 0
	for _, s := range shapes() {
		for _, r := range routes() {
			cells++
			t.Run(s.name+"/"+r.name, func(t *testing.T) {
				f := newFixture(t)
				path := s.prepare(t, f)
				before := mustSnapshot(t, path)
				if before.protected != s.protected || before.autoInherited != s.autoInherited {
					t.Logf("prepared with SE_DACL_PROTECTED=%v SE_DACL_AUTO_INHERITED=%v, aimed at %v and %v: the cell runs on %s",
						before.protected, before.autoInherited, s.protected, s.autoInherited, before.sddl)
				}
				rec := &recording{}
				rec.note("before", path)
				report := cellReport{shape: s.name, route: r.name, before: before}
				failed := false
				for i, data := range []string{"first", "second"} {
					if err := r.run(t, path, data, rec); err != nil {
						t.Errorf("write %d: %v%s", i+1, err, rec)
						failed = true
						break
					}
					rec.note(fmt.Sprintf("after write %d", i+1), path)
					if got, err := os.ReadFile(path); err != nil || string(got) != data {
						t.Errorf("write %d left %q (%v), want %q", i+1, got, err, data)
						failed = true
					}
					report.after[i] = mustSnapshot(t, path)
					report.changes[i] = changed(before, report.after[i])
				}
				reports = append(reports, report)
				if r.asserted && (report.changes[0] != "" || report.changes[1] != "") {
					t.Errorf("the write changed the security descriptor (first write: [%s], second write: [%s]):%s", report.changes[0], report.changes[1], rec)
					failed = true
				}
				if failed && !r.asserted {
					t.Logf("the recorded route differs (it is not asserted):%s", rec)
				}
			})
		}
	}
	t.Logf("matrix: %d cells", cells)
	if t.Failed() {
		var table strings.Builder
		for _, report := range reports {
			table.WriteString("\n" + report.String())
		}
		t.Logf("MATRIX of every cell (before, after the first write, after the second write):%s", table.String())
		probeCarryRoutes(t)
	}
}

// TestWindowsWriteFileOfANewFileGivesTheStoresDescriptor: a file that does not exist yet gets the descriptor AuthStorage gives its own file, and a second
// write leaves it as it is.
func TestWindowsWriteFileOfANewFileGivesTheStoresDescriptor(t *testing.T) {
	dir := t.TempDir()
	reference := filepath.Join(dir, "reference.json")
	createAsTheStoreDoes(t, reference)
	want := mustSnapshot(t, reference)
	path := filepath.Join(dir, "new.json")
	for i, data := range []string{"first", "second"} {
		if err := WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := mustSnapshot(t, path); got.sddl != want.sddl {
			t.Fatalf("after write %d (a new file at the first write): changed [%s]\n   store's file %s\n   written file %s", i+1, changed(want, got), want.sddl, got.sddl)
		}
	}
}

// TestWindowsInheritanceDoesNotReachAProtectedFileAfterAWrite decides what an auto-inherited flag on a protected DACL means for access. A file whose DACL is
// protected and holds an owner-only entry is written; then the directory that holds it gets an inheritable entry for Everyone through
// SetNamedSecurityInfo, the call that propagates inheritance to existing children. The file's entries must not change and Everyone must get nothing.
func TestWindowsInheritanceDoesNotReachAProtectedFileAfterAWrite(t *testing.T) {
	for _, r := range routes() {
		t.Run(r.name, func(t *testing.T) {
			message, reached := inheritanceVerdict(t, r)
			t.Log(message)
			if reached {
				t.Error(message)
			}
		})
	}
}

// inheritanceVerdict runs the test above for one route and returns what it saw (the verdict and the descriptors) and whether inheritance reached the file.
// The matrix prints it with its probes, so that a run in which a cell differs shows it too: a passing test prints nothing.
func inheritanceVerdict(t testing.TB, r route) (message string, reached bool) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	createAsTheStoreDoes(t, path)
	before := mustSnapshot(t, path)
	rec := &recording{}
	if err := r.run(t, path, "written", rec); err != nil {
		t.Fatalf("%v%s", err, rec)
	}
	written := mustSnapshot(t, path)
	testenv.InheritOthersRead(t, dir)
	propagated := mustSnapshot(t, path)
	verdict := "inheritance did not reach the protected file"
	if written.autoInherited {
		verdict = "inheritance did not reach the protected auto-inherited file"
	}
	if propagated.aces != written.aces || propagated.protected != written.protected || strings.Contains(propagated.aces, ";WD)") {
		reached = true
		verdict = "inheritance reached a protected file"
		if written.autoInherited {
			verdict = "inheritance reached a protected auto-inherited file"
		}
	}
	if ownerOnly, err := ownerfile.OwnerOnly(path, nil); err != nil || !ownerOnly {
		t.Errorf("owner-only after the propagation = %v (%v), want true", ownerOnly, err)
	}
	return fmt.Sprintf("%s (%s)\n   before the write      %s\n   after the write       %s\n   after the parent got an inheritable Everyone entry %s", verdict, r.name, before.sddl, written.sddl, propagated.sddl), reached
}

// carryProbe applies the descriptor of the file at from to the file at to through one Win32 call.
type carryProbe struct {
	name  string
	apply func(from, to string) error
}

func readParts(path string) (*windows.SECURITY_DESCRIPTOR, error) {
	return windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
}

func openForSecurity(path string, access uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
}

var procSetFileSecurity = windows.NewLazySystemDLL("advapi32.dll").NewProc("SetFileSecurityW")

func carryProbes() []carryProbe {
	return []carryProbe{
		{"SetKernelObjectSecurity (handle with WRITE_DAC, the descriptor's own control bits)", func(from, to string) error {
			sd, err := readParts(from)
			if err != nil {
				return err
			}
			handle, err := openForSecurity(to, windows.WRITE_DAC)
			if err != nil {
				return err
			}
			defer func() { _ = windows.CloseHandle(handle) }()
			return windows.SetKernelObjectSecurity(handle, windows.DACL_SECURITY_INFORMATION, sd)
		}},
		{"SetFileSecurityW (the descriptor's own control bits)", func(from, to string) error {
			sd, err := readParts(from)
			if err != nil {
				return err
			}
			name, err := windows.UTF16PtrFromString(to)
			if err != nil {
				return err
			}
			//nolint:gosec // G103: the pointers address a UTF-16 buffer and a descriptor that live across the call.
			if result, _, callErr := procSetFileSecurity.Call(uintptr(unsafe.Pointer(name)), uintptr(windows.DACL_SECURITY_INFORMATION), uintptr(unsafe.Pointer(sd))); result == 0 {
				return callErr
			}
			return nil
		}},
		{"SetKernelObjectSecurity with SE_DACL_AUTO_INHERIT_REQ|SE_DACL_AUTO_INHERITED in the control and the protected flag named (NOT SHOWN BEFORE)", func(from, to string) error {
			sd, err := readParts(from)
			if err != nil {
				return err
			}
			want, err := accessOf(sd)
			if err != nil {
				return err
			}
			return setAccessAsked(from, to, want)
		}},
		{"SetNamedSecurityInfo (DACL and the protected flag of the descriptor)", func(from, to string) error {
			sd, err := readParts(from)
			if err != nil {
				return err
			}
			dacl, _, err := sd.DACL()
			if err != nil {
				return err
			}
			control, _, err := sd.Control()
			if err != nil {
				return err
			}
			info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
			if control&windows.SE_DACL_PROTECTED != 0 {
				info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
			} else {
				info |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
			}
			return windows.SetNamedSecurityInfo(to, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil)
		}},
		{"carryDescriptor onto a temporary file (the three calls in their order, owner and group where they differ, read back)", func(from, to string) error { return carryDescriptor(from, to) }},
	}
}

// probeCarryRoutes prints, for every starting descriptor, the descriptor a fresh store-created file ends with when each Win32 call carries the starting
// descriptor onto it, and what MoveFileEx does to a file that carries it. It runs only after a cell has differed.
func probeCarryRoutes(t *testing.T) {
	t.Helper()
	var rows []string
	for _, s := range shapes() {
		t.Run("probe/"+s.name, func(t *testing.T) {
			f := newFixture(t)
			source := s.prepare(t, f)
			rows = append(rows, "  starting descriptor: "+s.name+"\n    source                          "+describe(source))
			for _, probe := range carryProbes() {
				target := filepath.Join(f.inheriting, "target-"+fmt.Sprint(len(rows))+".json")
				createAsTheStoreDoes(t, target)
				before := describe(target)
				err := probe.apply(source, target)
				rows = append(rows, fmt.Sprintf("    %s\n      target before %s\n      target after  %s (error: %v)", probe.name, before, describe(target), err))
			}
			carried := filepath.Join(f.inheriting, "carried.json")
			createAsTheStoreDoes(t, carried)
			victim := filepath.Join(f.inheriting, "victim.json")
			createAsTheStoreDoes(t, victim)
			carryErr := carryProbes()[0].apply(source, carried)
			rows = append(rows, fmt.Sprintf("    MoveFileEx of a file that carried the descriptor through SetKernelObjectSecurity over a store-created file (carry error: %v)\n      moved file before %s", carryErr, describe(carried)))
			moveErr := moveFile(carried, victim)
			rows = append(rows, fmt.Sprintf("      victim after the move %s (error: %v)", describe(victim), moveErr))
		})
	}
	for _, r := range routes() {
		t.Run("probe/inheritance/"+r.name, func(t *testing.T) {
			message, _ := inheritanceVerdict(t, r)
			rows = append(rows, "  the parent-inheritable-Everyone test, "+message)
		})
	}
	t.Logf("PROBES of the other routes on fresh files:\n%s", strings.Join(rows, "\n"))
}
