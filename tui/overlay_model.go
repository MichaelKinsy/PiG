package tui

import (
	"cmp"
	"slices"
)

type overlayID uint64

type overlayFrame struct {
	geometryGeneration uint64
	sequence           uint64
	lines              []string
	bytes              int
}

type overlayEntry struct {
	id               overlayID
	parentID         overlayID
	component        Component
	preFocus         Component
	opts             OverlayOptions
	mounted          bool
	hidden           bool
	evaluatedVisible bool
	appendSequence   uint64
	focusOrder       uint64
	hasFrame         bool
	snapshotBacked   bool
	frame            overlayFrame
}

type overlayFocusRestoreKind uint8

const (
	overlayFocusInactive overlayFocusRestoreKind = iota
	overlayFocusEligible
	overlayFocusBlocked
)

type overlayFocusRestore struct {
	kind           overlayFocusRestoreKind
	overlay        overlayID
	blockedBy      Component
	resumeTarget   Component
	resumeExplicit bool
}

type overlayModel struct {
	nextID             overlayID
	nextAppend         uint64
	nextFocusOrder     uint64
	geometryWidth      int
	geometryHeight     int
	geometryGeneration uint64
	entries            []*overlayEntry
	focused            overlayID
	focusTarget        Component
	pendingRestore     overlayID
	focusRestore       overlayFocusRestore
}

// overlayStateSnapshot is detached from the mutable owner-loop model. Entry
// values are copied so render and input consumers cannot observe a partially
// applied command.
type overlayStateSnapshot struct {
	Entries       []overlayEntry
	Focused       overlayID
	MountedAny    bool
	VisibleAny    bool
	RetainedBytes int
}

func (m *overlayModel) mount(component Component, opts OverlayOptions, parentID overlayID, visible bool) overlayID {
	if parentID != 0 && m.overlayByID(parentID) == nil {
		return 0
	}
	m.nextID++
	m.nextAppend++
	m.nextFocusOrder++
	entry := &overlayEntry{
		id:               m.nextID,
		parentID:         parentID,
		component:        component,
		preFocus:         m.focusedComponent(),
		opts:             opts,
		mounted:          true,
		evaluatedVisible: visible,
		appendSequence:   m.nextAppend,
		focusOrder:       m.nextFocusOrder,
	}
	m.entries = append(m.entries, entry)
	if !opts.nonCapturing && visible {
		m.focused = entry.id
		m.focusTarget = entry.focusComponent()
		m.markFocusEligible(entry.id)
	}
	return entry.id
}

func (m *overlayModel) setEvaluatedVisible(id overlayID, visible bool) bool {
	entry := m.overlayByID(id)
	if entry == nil || entry.evaluatedVisible == visible {
		return false
	}
	entry.evaluatedVisible = visible
	if !visible && m.focused == id {
		m.pendingRestore = id
		m.restoreFocus(entry, 0)
	} else if visible && !entry.hidden && !entry.opts.nonCapturing && (m.pendingRestore == id || m.focused == 0) {
		m.focused = id
		m.focusTarget = entry.focusComponent()
		m.pendingRestore = 0
		m.markFocusEligible(id)
	}
	return true
}

func (m *overlayModel) replaceSnapshot(id overlayID, geometryGeneration, sequence uint64, lines []string, visible bool) bool {
	entry := m.overlayByID(id)
	if entry == nil || geometryGeneration != m.geometryGeneration || sequence <= entry.frame.sequence {
		return false
	}
	frame := overlayFrame{
		geometryGeneration: geometryGeneration,
		sequence:           sequence,
		lines:              append([]string(nil), lines...),
	}
	for _, line := range frame.lines {
		frame.bytes += len(line)
	}
	entry.frame = frame
	entry.hasFrame = true
	entry.snapshotBacked = true
	entry.evaluatedVisible = visible
	if !visible && m.focused == id {
		m.pendingRestore = id
		m.restoreFocus(entry, 0)
	} else if visible && !entry.hidden && !entry.opts.nonCapturing && (m.pendingRestore == id || m.focused == 0) {
		m.focused = id
		m.focusTarget = entry.focusComponent()
		m.pendingRestore = 0
		m.markFocusEligible(id)
	}
	return true
}

func (m *overlayModel) updateGeometry(width, height int) uint64 {
	if width == m.geometryWidth && height == m.geometryHeight && m.geometryGeneration != 0 {
		return m.geometryGeneration
	}
	m.geometryWidth = width
	m.geometryHeight = height
	m.geometryGeneration++
	for _, entry := range m.entries {
		if entry.snapshotBacked && entry.frame.geometryGeneration != m.geometryGeneration {
			entry.evaluatedVisible = false
			entry.hasFrame = false
			entry.frame.lines = nil
			entry.frame.bytes = 0
		}
	}
	if focused := m.overlayByID(m.focused); focused != nil && !focused.visible() {
		m.pendingRestore = focused.id
		m.restoreFocus(focused, 0)
	}
	return m.geometryGeneration
}

func (m *overlayModel) removeTree(parent overlayID) bool {
	if m.overlayByID(parent) == nil {
		return false
	}
	removed := map[overlayID]bool{parent: true}
	for changed := true; changed; {
		changed = false
		for _, entry := range m.entries {
			if !removed[entry.id] && removed[entry.parentID] {
				removed[entry.id] = true
				changed = true
			}
		}
	}
	restoreEntry := m.overlayByID(parent)
	kept := m.entries[:0]
	for _, entry := range m.entries {
		if removed[entry.id] {
			m.retargetPreFocus(entry)
			entry.mounted = false
			entry.frame.lines = nil
			continue
		}
		kept = append(kept, entry)
	}
	clear(m.entries[len(kept):])
	m.entries = kept
	if removed[m.focused] {
		m.restoreFocus(restoreEntry, 0)
	}
	if removed[m.pendingRestore] {
		m.pendingRestore = 0
	}
	if removed[m.focusRestore.overlay] {
		m.clearFocusRestore()
	}
	return true
}

func (m *overlayModel) setHidden(id overlayID, hidden bool) bool {
	entry := m.overlayByID(id)
	if entry == nil || entry.hidden == hidden {
		return false
	}
	entry.hidden = hidden
	if hidden {
		m.clearFocusRestoreFor(id)
	}
	if hidden && m.focused == id {
		m.pendingRestore = 0
		m.restoreFocus(entry, 0)
	} else if !hidden && entry.evaluatedVisible && !entry.opts.nonCapturing {
		m.nextFocusOrder++
		entry.focusOrder = m.nextFocusOrder
		m.focused = id
		m.focusTarget = entry.focusComponent()
		m.pendingRestore = 0
		m.markFocusEligible(id)
	}
	return true
}

func (m *overlayModel) isHidden(id overlayID) bool {
	entry := m.overlayByID(id)
	return entry != nil && entry.hidden
}

func (m *overlayModel) unfocus(id overlayID, target Component, explicit bool) bool {
	entry := m.overlayByID(id)
	if entry == nil {
		return false
	}
	restore := m.focusRestore
	isFocused := m.focused == id
	hasPendingRestore := restore.kind != overlayFocusInactive && restore.overlay == id
	if !isFocused && !hasPendingRestore {
		return false
	}
	if restore.kind == overlayFocusBlocked && restore.overlay == id && restore.blockedBy == m.focusTarget {
		if explicit {
			m.focusRestore.resumeExplicit = true
			m.focusRestore.resumeTarget = target
		} else {
			m.clearFocusRestore()
		}
		return true
	}
	m.clearFocusRestoreFor(id)
	m.pendingRestore = 0
	if explicit {
		m.focused = m.componentID(target)
		m.focusTarget = target
		if m.focused != 0 {
			m.markFocusEligible(m.focused)
		}
		return true
	}
	if isFocused {
		m.restoreFocus(entry, id)
	}
	return true
}

func (m *overlayModel) isFocused(id overlayID) bool {
	return m.focused == id && m.overlayByID(id) != nil
}

func (m *overlayModel) componentID(component Component) overlayID {
	id := m.mountedComponentID(component)
	entry := m.overlayByID(id)
	if entry == nil || !entry.visible() {
		return 0
	}
	return id
}

func (m *overlayModel) mountedComponentID(component Component) overlayID {
	for _, entry := range m.entries {
		if entry.focusComponent() == component {
			return entry.id
		}
	}
	return 0
}

func (m *overlayModel) focus(id overlayID) bool {
	entry := m.overlayByID(id)
	if entry == nil || !entry.visible() {
		return false
	}
	m.nextFocusOrder++
	entry.focusOrder = m.nextFocusOrder
	m.focused = id
	m.focusTarget = entry.focusComponent()
	m.pendingRestore = 0
	m.markFocusEligible(id)
	return true
}

func (m *overlayModel) removeTarget(id overlayID) bool {
	for i, entry := range m.entries {
		if entry.id != id || !entry.mounted {
			continue
		}
		entry.mounted = false
		m.retargetPreFocus(entry)
		m.entries = slices.Delete(m.entries, i, i+1)
		if m.focused == id {
			m.restoreFocus(entry, 0)
		}
		if m.pendingRestore == id {
			m.pendingRestore = 0
		}
		m.clearFocusRestoreFor(id)
		return true
	}
	return false
}

func (m *overlayModel) removeAppendTail() bool {
	if len(m.entries) == 0 {
		return false
	}
	return m.removeTarget(m.entries[len(m.entries)-1].id)
}

func (m *overlayModel) overlayByID(id overlayID) *overlayEntry {
	for _, entry := range m.entries {
		if entry.id == id && entry.mounted {
			return entry
		}
	}
	return nil
}

func (m *overlayModel) frontmostEligibleExcept(except overlayID) overlayID {
	var id overlayID
	var order uint64
	for _, entry := range m.entries {
		if entry.id == except || entry.opts.nonCapturing || !entry.visible() || entry.focusOrder < order {
			continue
		}
		id = entry.id
		order = entry.focusOrder
	}
	return id
}

func (m *overlayModel) retargetPreFocus(removed *overlayEntry) {
	removedComponent := removed.focusComponent()
	for _, entry := range m.entries {
		if entry != removed && entry.preFocus == removedComponent {
			entry.preFocus = removed.preFocus
		}
	}
}

func (m *overlayModel) setFocusTarget(component Component, previousFocusMounted bool) bool {
	previousTarget := m.focusTarget
	previousOverlay := m.overlayByID(m.focused)
	nextTarget := component
	nextID := m.mountedComponentID(nextTarget)
	restore := m.visibleFocusRestore()

	if nextTarget != nil && nextID == 0 {
		switch {
		case restore.kind == overlayFocusBlocked && restore.blockedBy == previousTarget:
			if restore.resumeExplicit || !previousFocusMounted {
				nextTarget, nextID = m.resolveBlockedFocus(restore)
			} else {
				m.focusRestore.blockedBy = nextTarget
			}
		case previousOverlay != nil && restore.kind != overlayFocusInactive && restore.overlay == previousOverlay.id && !m.isOverlayFocusAncestor(previousOverlay, nextTarget):
			m.focusRestore = overlayFocusRestore{
				kind: overlayFocusBlocked, overlay: previousOverlay.id,
				blockedBy: nextTarget,
			}
		}
	} else if nextTarget == nil {
		if restore.kind == overlayFocusBlocked && restore.blockedBy == previousTarget {
			nextTarget, nextID = m.resolveBlockedFocus(restore)
		} else {
			m.clearFocusRestore()
		}
	}

	changed := m.focused != nextID || m.focusTarget != nextTarget
	m.focused = nextID
	m.focusTarget = nextTarget
	m.pendingRestore = 0
	if next := m.overlayByID(nextID); next != nil && next.visible() {
		m.focusRestore = overlayFocusRestore{kind: overlayFocusEligible, overlay: nextID}
	}
	return changed
}

func (m *overlayModel) markFocusEligible(id overlayID) {
	m.focusRestore = overlayFocusRestore{kind: overlayFocusEligible, overlay: id}
}

func (m *overlayModel) clearFocusRestore() {
	m.focusRestore = overlayFocusRestore{}
}

func (m *overlayModel) clearFocusRestoreFor(id overlayID) {
	if m.focusRestore.overlay == id {
		m.clearFocusRestore()
	}
}

func (m *overlayModel) visibleFocusRestore() overlayFocusRestore {
	restore := m.focusRestore
	if restore.kind == overlayFocusInactive {
		return restore
	}
	entry := m.overlayByID(restore.overlay)
	if entry == nil || !entry.visible() {
		return overlayFocusRestore{}
	}
	return restore
}

func (m *overlayModel) resolveBlockedFocus(restore overlayFocusRestore) (Component, overlayID) {
	if !restore.resumeExplicit {
		entry := m.overlayByID(restore.overlay)
		if entry != nil {
			return entry.focusComponent(), entry.id
		}
	}
	m.clearFocusRestore()
	return restore.resumeTarget, m.componentID(restore.resumeTarget)
}

func (m *overlayModel) isOverlayFocusAncestor(entry *overlayEntry, component Component) bool {
	visited := map[Component]bool{}
	current := entry.preFocus
	for current != nil && !visited[current] {
		visited[current] = true
		if current == component {
			return true
		}
		id := m.mountedComponentID(current)
		ancestor := m.overlayByID(id)
		if ancestor == nil {
			return false
		}
		current = ancestor.preFocus
	}
	return false
}

func (m *overlayModel) focusedComponent() Component {
	return m.focusTarget
}

func (m *overlayModel) restoreFocus(entry *overlayEntry, except overlayID) {
	id := m.frontmostEligibleExcept(except)
	m.focused = id
	if focused := m.overlayByID(id); focused != nil {
		m.focusTarget = focused.focusComponent()
		m.markFocusEligible(id)
		return
	}
	if m.pendingRestore == 0 {
		m.clearFocusRestore()
	}
	if entry != nil {
		m.focusTarget = entry.preFocus
	} else {
		m.focusTarget = nil
	}
}

func (e *overlayEntry) focusComponent() Component {
	if modal, ok := e.component.(*modalOverlay); ok {
		return modal.component
	}
	return e.component
}

func (e *overlayEntry) visible() bool {
	return e.mounted && !e.hidden && e.evaluatedVisible
}

type overlayVisibilityCandidate struct {
	id       overlayID
	evaluate func(termWidth, termHeight int) bool
}

func (m *overlayModel) visibilityCandidates() []overlayVisibilityCandidate {
	var candidates []overlayVisibilityCandidate
	for _, entry := range m.entries {
		if entry.snapshotBacked || entry.opts.visible == nil {
			continue
		}
		candidates = append(candidates, overlayVisibilityCandidate{id: entry.id, evaluate: entry.opts.visible})
	}
	return candidates
}

func (m *overlayModel) mountedAny() bool { return len(m.entries) > 0 }

func (m *overlayModel) visibleAny() bool {
	for _, entry := range m.entries {
		if entry.visible() {
			return true
		}
	}
	return false
}

func (m *overlayModel) snapshot() overlayStateSnapshot {
	snapshot := overlayStateSnapshot{
		Entries:    make([]overlayEntry, 0, len(m.entries)),
		Focused:    m.focused,
		MountedAny: len(m.entries) > 0,
	}
	for _, entry := range m.entries {
		copy := *entry
		snapshot.RetainedBytes += entry.frame.bytes
		snapshot.Entries = append(snapshot.Entries, copy)
		snapshot.VisibleAny = snapshot.VisibleAny || copy.visible()
	}
	slices.SortStableFunc(snapshot.Entries, func(a, b overlayEntry) int {
		return cmp.Compare(a.focusOrder, b.focusOrder)
	})
	return snapshot
}

func (s overlayStateSnapshot) entryByID(id overlayID) (overlayEntry, bool) {
	for i := range s.Entries {
		if s.Entries[i].id == id {
			return s.Entries[i], true
		}
	}
	return overlayEntry{}, false
}
