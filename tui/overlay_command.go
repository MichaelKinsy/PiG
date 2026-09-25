package tui

type overlayCommandKind uint8

const (
	overlayMount overlayCommandKind = iota + 1
	overlayNestedMount
	overlayRemoveTarget
	overlayRemoveAppendTail
	overlaySetHidden
	overlaySetEvaluatedVisible
	overlaySetFocusTarget
	overlayFocus
	overlayUnfocus
	overlayGeometryChanged
	overlayReplaceSnapshot
	overlayParentTeardown
)

// overlayCommand is the internal owner-loop seam. It contains state intent,
// stable identities, immutable visual data, and local component references; it
// has no extension protocol or transport dependency.
type overlayCommand struct {
	kind      overlayCommandKind
	entryID   overlayID
	parentID  overlayID
	component Component
	options   OverlayOptions

	hidden               bool
	explicitTarget       bool
	target               Component
	previousFocusMounted bool

	width, height      int
	geometryGeneration uint64
	frameSequence      uint64
	lines              []string
	visible            bool
}

type overlayCommandResult struct {
	entryID            overlayID
	changed            bool
	geometryGeneration uint64
}

func (m *overlayModel) apply(command overlayCommand) overlayCommandResult {
	switch command.kind {
	case overlayMount:
		id := m.mount(command.component, command.options, 0, command.visible)
		return overlayCommandResult{entryID: id, changed: id != 0}
	case overlayNestedMount:
		id := m.mount(command.component, command.options, command.parentID, command.visible)
		return overlayCommandResult{entryID: id, changed: id != 0}
	case overlayRemoveTarget:
		return overlayCommandResult{changed: m.removeTarget(command.entryID)}
	case overlayRemoveAppendTail:
		return overlayCommandResult{changed: m.removeAppendTail()}
	case overlaySetHidden:
		return overlayCommandResult{changed: m.setHidden(command.entryID, command.hidden)}
	case overlaySetEvaluatedVisible:
		return overlayCommandResult{changed: m.setEvaluatedVisible(command.entryID, command.visible)}
	case overlaySetFocusTarget:
		return overlayCommandResult{changed: m.setFocusTarget(command.target, command.previousFocusMounted)}
	case overlayFocus:
		return overlayCommandResult{changed: m.focus(command.entryID)}
	case overlayUnfocus:
		return overlayCommandResult{changed: m.unfocus(command.entryID, command.target, command.explicitTarget)}
	case overlayGeometryChanged:
		generation := m.updateGeometry(command.width, command.height)
		return overlayCommandResult{changed: true, geometryGeneration: generation}
	case overlayReplaceSnapshot:
		return overlayCommandResult{changed: m.replaceSnapshot(
			command.entryID,
			command.geometryGeneration,
			command.frameSequence,
			command.lines,
			command.visible,
		)}
	case overlayParentTeardown:
		return overlayCommandResult{changed: m.removeTree(command.parentID)}
	default:
		return overlayCommandResult{}
	}
}

type overlayInputSnapshot struct {
	entryID   overlayID
	component Component
	eligible  bool
}

func (m *overlayModel) prepareInput(blockerMounted bool) {
	restore := m.visibleFocusRestore()
	switch restore.kind {
	case overlayFocusEligible:
		if m.focused != restore.overlay {
			if entry := m.overlayByID(restore.overlay); entry != nil {
				m.focused = entry.id
				m.focusTarget = entry.focusComponent()
			}
		}
	case overlayFocusBlocked:
		if m.focusTarget != restore.blockedBy || !blockerMounted {
			target, id := m.resolveBlockedFocus(restore)
			m.focused = id
			m.focusTarget = target
			if id != 0 {
				m.markFocusEligible(id)
			}
		}
	}
}

func (m *overlayModel) inputSnapshot() overlayInputSnapshot {
	entry := m.overlayByID(m.focused)
	if entry == nil || !entry.visible() {
		return overlayInputSnapshot{}
	}
	return overlayInputSnapshot{entryID: entry.id, component: entry.focusComponent(), eligible: true}
}
