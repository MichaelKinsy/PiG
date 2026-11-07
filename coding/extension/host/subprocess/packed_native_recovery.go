package subprocess

import (
	"bytes"
	"context"
	"fmt"
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

// nativeCulprit is the member the process's crash trace names, or "" when it names none or more than one.
func (p *packedProcessState) nativeCulprit() string {
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
	return traceCulprit(readStderrTail(log), members)
}

func traceCulprit(trace []byte, members []nativeMember) string {
	if len(members) == 0 {
		return ""
	}
	switch members[0].language {
	case "go":
		return goTraceCulprit(trace, members)
	case "lisp":
		return lispTraceCulprit(trace, members)
	}
	return ""
}

// goTraceCulprit reads the goroutine that was running when the process died ("goroutine N [running]:") from the innermost frame out, the goroutine's
// creator last ("created by pkg.fn in goroutine M"), and returns the member that owns the first frame it can attribute. A frame belongs to the member whose
// import path is its package's, else to the one member whose module holds the package. The SDK's and the runner's frames name nobody.
func goTraceCulprit(trace []byte, members []nativeMember) string {
	inRunning := false
	for rawLine := range bytes.Lines(trace) {
		line := strings.TrimRight(string(rawLine), "\r\n")
		switch {
		case strings.HasPrefix(line, "goroutine ") && strings.HasSuffix(line, ":"):
			if inRunning {
				return ""
			}
			inRunning = strings.Contains(line, "[running")
			continue
		case !inRunning, line == "", line[0] == '\t', line[0] == ' ':
			continue
		}
		function, created := strings.CutPrefix(line, "created by ")
		if !created && !strings.Contains(line, "(") {
			continue
		}
		if name := goFramePackageOwner(function, members); name != "" {
			return name
		}
	}
	return ""
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

// lispTraceCulprit reads the backtrace SBCL prints for the thread an unhandled condition ended ("Backtrace for: ..."), innermost frame first, and returns
// the member whose package qualifies the first symbol it can attribute.
func lispTraceCulprit(trace []byte, members []nativeMember) string {
	inBacktrace := false
	for rawLine := range bytes.Lines(trace) {
		line := strings.TrimRight(string(rawLine), "\r\n")
		if strings.HasPrefix(line, "Backtrace for:") {
			inBacktrace = true
			continue
		}
		if !inBacktrace {
			continue
		}
		// SBCL prints a package whose name is not upper case between bars (|alpha|::NEW-EXTENSION); the reader folds the name to upper case.
		upper := strings.ToUpper(strings.ReplaceAll(line, "|", ""))
		for _, member := range members {
			if member.lispPkg != "" && lispSymbolIn(upper, member.lispPkg) {
				return member.name
			}
		}
	}
	return ""
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
