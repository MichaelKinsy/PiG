package subprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"unicode"
)

// A packed Go or Lisp process ends when code that no recover or handler reaches fails on a thread of its own: a goroutine an extension started, a Lisp thread it
// made. Its trace names the extension, because the frames of the failing thread are the extension's own functions. The host blames that one member, stops it as an
// isolated extension stops when its process ends, restarts the others in a shared process as Node recovery does (recoverNodeProcess), and falls back to stopping the whole cell, which a
// /reload restarts, when no trace names a member (a kill, os.Exit, a runtime fatal error).

// nativeMember is what a crash trace can name a member by: its Go import path and module, or its Lisp package.
type nativeMember struct {
	name       string
	language   string
	importPath string
	modulePath string
	lispPkg    string
}

func (p *packedProcessState) nativeMembers() []nativeMember {
	var members []nativeMember
	for _, cfg := range p.configs {
		members = append(members, nativeMember{name: cfg.Name, language: cfg.RuntimeLanguage, importPath: cfg.Package, modulePath: cfg.ModulePath, lispPkg: strings.ToUpper(cfg.Package)})
	}
	return members
}

// nativeCulprit is the member the process's crash trace names, or "" when it names none or more than one, or when the process did not end the way that trace
// says: waitErr is what the process's wait returned.
func (p *packedProcessState) nativeCulprit(waitErr error) string {
	if p.node || len(p.members) == 0 {
		return ""
	}
	members := p.nativeMembers()
	log := ""
	for _, me := range p.members {
		if me.stderrLogPath != "" {
			log = me.stderrLogPath
			break
		}
	}
	return traceCulprit(readStderrTail(log), members, processExitCode(waitErr))
}

// processExitCode is the exit status a wait error reports, or -1 for a process a signal ended or a wait that failed.
func processExitCode(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](waitErr); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func traceCulprit(trace []byte, members []nativeMember, exitCode int) string {
	if len(members) == 0 {
		return ""
	}
	switch members[0].language {
	case "go":
		return goTraceCulprit(trace, members, exitCode)
	case "lisp":
		return lispTraceCulprit(trace, members)
	}
	return ""
}

// goPanicExitStatus is the exit status of a Go process that an unrecovered panic or a runtime fatal error ended.
const goPanicExitStatus = 2

// goTraceCulprit reads the trace of the panic that ended the process. It reads one only when the process ended with Go's status for a panic (2), and the log's
// last line that begins "panic: " starts the runtime's section: its message and continuation lines up to the blank line, then the header of the goroutine that
// was running ("goroutine N [running]:") with nothing written between. A process that ended any other way names no one: os.Exit and a kill leave the text a
// member logged for a panic it recovered (the common "panic: %v\n\n%s" with debug.Stack()) as the last such line, and a runtime fatal error ("fatal error: "
// after the last "panic: " line) ends with status 2 as a panic does. Text a goroutine wrote between the panic's line and its goroutine header (a goroutine
// header inside the message block) means the section is not the runtime's alone, and names no one.
// The goroutine's frames, innermost first, and its creator ("created by pkg.fn in goroutine M") name the member whose package is a frame's: its import
// path, else the one member whose module holds the package. The SDK's and the runner's frames name nobody. Nothing before the section decides: the blocks the
// SDK writes for the panics it recovered ("goroutine N [running]:" after "extension: recovered from panic ...") are handled failures. A trace that names two
// members names no one.
func goTraceCulprit(trace []byte, members []nativeMember, exitCode int) string {
	if exitCode != goPanicExitStatus {
		return ""
	}
	var lines []string
	for rawLine := range bytes.Lines(trace) {
		lines = append(lines, strings.TrimRight(string(rawLine), "\r\n"))
	}
	section := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "panic: ") {
			section = i
		}
	}
	if section < 0 {
		return ""
	}
	if slices.ContainsFunc(lines[section+1:], func(line string) bool { return strings.HasPrefix(line, "fatal error: ") }) {
		return ""
	}
	next := section + 1
	for ; next < len(lines) && lines[next] != ""; next++ {
		if isGoroutineHeader(lines[next]) {
			return ""
		}
	}
	next++
	if next >= len(lines) || !isGoroutineHeader(lines[next]) || !strings.Contains(lines[next], "[running") {
		return ""
	}
	owners := map[string]bool{}
	for _, line := range lines[next+1:] {
		switch {
		case isGoroutineHeader(line):
			return soleOwner(owners)
		case line == "", line[0] == '\t', line[0] == ' ':
			continue
		}
		function, created := strings.CutPrefix(line, "created by ")
		if !created && !strings.Contains(line, "(") {
			continue
		}
		if name := goFramePackageOwner(function, members); name != "" {
			owners[name] = true
		}
	}
	return soleOwner(owners)
}

func isGoroutineHeader(line string) bool {
	return strings.HasPrefix(line, "goroutine ") && strings.HasSuffix(line, ":")
}

// goFramePackageOwner returns the member that owns the package of a stack frame's function name ("example.com/ext.(*T).Method(0xc0)" has package
// example.com/ext), or "".
func goFramePackageOwner(function string, members []nativeMember) string {
	function, _, _ = strings.Cut(function, " in goroutine ")
	// The name ends where the receiver or the arguments begin: "pkg.(*T).Method(0xc0)" and "pkg.Func(0xc0)" both have package pkg.
	head, _, _ := strings.Cut(function, "(")
	slash := strings.LastIndex(head, "/")
	dot := strings.Index(head[slash+1:], ".")
	if dot < 0 {
		return ""
	}
	pkg := head[:slash+1+dot]
	var byModule []string
	for _, member := range members {
		if member.importPath != "" && pkg == member.importPath {
			return member.name
		}
		if member.modulePath != "" && (pkg == member.modulePath || strings.HasPrefix(pkg, member.modulePath+"/")) {
			byModule = append(byModule, member.name)
		}
	}
	if len(byModule) == 1 {
		return byModule[0]
	}
	return ""
}

// lispQuitLine is what SBCL writes, after the backtrace of the thread an unhandled condition ended, when it quits in --disable-debugger mode.
const lispQuitLine = "unhandled condition in --disable-debugger mode, quitting"

// lispTraceCulprit reads the backtrace SBCL prints for the thread an unhandled condition ended: the last "Backtrace for: ..." section, and only when SBCL's quit
// line follows it. A process that ended any other way names no one: a member that printed a backtrace itself (sb-debug:print-backtrace writes the same header)
// and a process that then ended by sb-ext:exit or a kill leave no quit line after it. The one member whose package qualifies the function of one of the
// section's frames is returned. Only a frame's function counts: the arguments SBCL prints after it include the condition's text ("3: (ERROR \"sibling: connection
// lost\")"), which names nobody. Frames that name two members, or none, name no one.
func lispTraceCulprit(trace []byte, members []nativeMember) string {
	var frames []string
	inBacktrace, quit := false, false
	for rawLine := range bytes.Lines(trace) {
		line := strings.TrimRight(string(rawLine), "\r\n")
		if strings.HasPrefix(line, "Backtrace for:") {
			frames, inBacktrace, quit = frames[:0], true, false
			continue
		}
		if line == lispQuitLine {
			quit = inBacktrace
			continue
		}
		if frame, ok := lispFrameFunction(line); ok && inBacktrace && !quit {
			frames = append(frames, frame)
		}
	}
	if !quit {
		return ""
	}
	owners := map[string]bool{}
	for _, frame := range frames {
		// SBCL prints a package whose name is not upper case between bars (|alpha|::NEW-EXTENSION); the reader folds the name to upper case.
		upper := strings.ToUpper(strings.ReplaceAll(frame, "|", ""))
		for _, member := range members {
			if member.lispPkg != "" && lispSymbolIn(upper, member.lispPkg) {
				owners[member.name] = true
			}
		}
	}
	return soleOwner(owners)
}

// soleOwner is the one member of owners, or "" when it holds none or several: a trace that names two members stops the whole cell.
func soleOwner(owners map[string]bool) string {
	if len(owners) != 1 {
		return ""
	}
	for name := range owners {
		return name
	}
	return ""
}

// lispFrameFunction returns the function of a backtrace frame line "N: (FUNCTION args...)" or "N: ((LAMBDA ... :IN PKG::FN) args...)", without the string
// literals SBCL prints in it, and whether the line is a frame.
func lispFrameFunction(line string) (string, bool) {
	number, rest, ok := strings.Cut(line, ": ")
	if !ok || number == "" || strings.Trim(number, "0123456789") != "" || !strings.HasPrefix(rest, "(") {
		return "", false
	}
	var stripped strings.Builder
	inString := false
	for i := 0; i < len(rest); i++ {
		switch c := rest[i]; {
		case inString && c == '\\':
			i++
		case c == '"':
			inString = !inString
		case !inString:
			stripped.WriteByte(c)
		}
	}
	rest = stripped.String()
	if strings.HasPrefix(rest, "((") {
		depth := 0
		for i := 1; i < len(rest); i++ {
			switch rest[i] {
			case '(':
				depth++
			case ')':
				if depth--; depth == 0 {
					return rest[1 : i+1], true
				}
			}
		}
		return rest[1:], true
	}
	function, _, _ := strings.Cut(rest[1:], " ")
	return strings.TrimRight(function, ")"), true
}

// lispSymbolIn reports whether line holds a symbol qualified by package pkg (upper case): "PKG:" or "PKG::" not preceded by a character of a longer name.
func lispSymbolIn(line, pkg string) bool {
	for from := 0; ; {
		at := strings.Index(line[from:], pkg+":")
		if at < 0 {
			return false
		}
		at += from
		if at == 0 || !lispSymbolChar(rune(line[at-1])) {
			return true
		}
		from = at + len(pkg)
	}
}

func lispSymbolChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_*+<>=!?$%&/.", r)
}

// recoverNativeProcess restarts the other members of a packed Go or Lisp process that a thread of culprit's own code ended, in a shared process. The culprit stops, as
// an isolated factory extension does when its process ends, and a /reload runs it isolated, never again beside members it took down. Interrupted calls are not
// replayed and a restarted member's in-memory state starts over.
// pig divergence (D274): the members that shared the process restart, which Pi's one process never does, and lose their in-memory state and in-flight calls.
func (h *Host) recoverNativeProcess(failed *packedProcessState, culprit, reason string) {
	h.transitionMu.Lock()
	defer h.transitionMu.Unlock()
	h.mu.Lock()
	if h.shuttingDown.Load() || failed.stopping.Load() || h.packedCellGeneration[failed.key] != failed.generation {
		h.mu.Unlock()
		return
	}
	// Invalidate the old process before any replacement may register. Its sockets retain no UI or provider authority.
	h.packedCellGeneration[failed.key]++
	failed.stopping.Store(true)
	members := slices.Clone(failed.members)
	//perf:ok cold: once per crash of a packed process, over its few members
	slices.SortFunc(members, func(a, b *managedExt) int { return h.loadOrder[a.config.Name] - h.loadOrder[b.config.Name] })
	for _, me := range members {
		if h.exts[me.config.Name] == me {
			delete(h.exts, me.config.Name)
		}
	}
	h.mu.Unlock()
	if h.nativeFaults == nil {
		h.nativeFaults = map[string]string{}
	}
	cause := stderrCause(failed.members[0].stderrLogPath, culprit)
	culpritReason := reason + "; " + culprit + "'s own code ended the shared process"
	if cause != "" {
		culpritReason += ": " + cause
	}
	h.nativeFaults[culprit] = culpritReason
	// Reserve the failed process's diagnostic before recovery teardown can remove it.
	logPath := ""
	if h.crashHandler() != nil {
		logPath = failed.stderrLog.retain()
	}
	configured := map[string]ExtConfig{}
	for _, cfg := range failed.configs {
		configured[cfg.Name] = cfg
	}
	var configs []ExtConfig
	oldByName := map[string]*managedExt{}
	for _, me := range members {
		oldByName[me.config.Name] = me
		h.stopManaged(me, "packed process recovery")
		if me.config.Name == culprit {
			continue
		}
		cfg, ok := configured[me.config.Name]
		if !ok {
			cfg = me.config
			cfg.Path = ""
		}
		cfg.RuntimeKind = "subprocess"
		cfg.EntrypointKind = "factory"
		configs = append(configs, cfg)
	}
	failed.stopAndReap()
	if report := h.crashHandler(); report != nil {
		report(culprit, 0, true, withStderrLog(culpritReason+"; "+culprit+" stopped; /reload restarts it", logPath))
		for _, cfg := range configs {
			report(cfg.Name, 0, false, withStderrLog(reason+"; "+culprit+" ended the shared process; restarting with the other members; interrupted callbacks are not replayed", logPath))
		}
	}
	lifetime := h.newRecoveryLifetime(failed.originalOwner)
	defer lifetime.release()
	ctx := context.WithValue(lifetime.context, recoveryLifetimeKey{}, lifetime)
	ctx = context.WithValue(ctx, runtimeParentKey{}, lifetime.context)
	ctx = context.WithValue(ctx, recoveryMembersKey{}, oldByName)
	if ctx.Err() != nil || h.shuttingDown.Load() {
		return
	}
	cells := h.planCells(configs)
	h.planEventBus(ctx, cells)
	var replacement []stagedManagedExt
	h.stageCellsInOrder(ctx, cells, func(cell CellSpec, outcome stageOutcome, err error) {
		outcomes, failures := h.isolateCellFailure(ctx, cell, nil, outcome, err)
		for _, failure := range failures {
			if report := h.crashHandler(); report != nil {
				report(failure.cfg.Name, 0, true, "recovery failed: "+failure.err.Error())
			}
		}
		for _, outcome := range outcomes {
			replacement = append(replacement, outcome.staged...)
		}
	})
	for _, item := range replacement {
		if old := oldByName[item.name]; old != nil {
			old.replacement.Store(item.me)
		}
	}
	h.commitStaged(replacement, nil, "packed process recovery")
}

// nativeFaultNotice names the culprit-isolation of a member in a reload report.
func nativeFaultNotice(reason string) string {
	return fmt.Sprintf("packed member isolated after its own code ended the shared process: %s", reason)
}
