package codingagent

// AppKeybinding is upstream AppKeybinding (core/keybindings.ts:60), keyof AppKeybindings: the closed set of the coding agent's own keybinding ids.
type AppKeybinding string

const (
	AppInterrupt                AppKeybinding = "app.interrupt"
	AppClear                    AppKeybinding = "app.clear"
	AppExit                     AppKeybinding = "app.exit"
	AppSuspend                  AppKeybinding = "app.suspend"
	AppThinkingCycle            AppKeybinding = "app.thinking.cycle"
	AppThinkingSave             AppKeybinding = "app.thinking.save"
	AppModelCycleForward        AppKeybinding = "app.model.cycleForward"
	AppModelCycleBackward       AppKeybinding = "app.model.cycleBackward"
	AppModelSelect              AppKeybinding = "app.model.select"
	AppToolsExpand              AppKeybinding = "app.tools.expand"
	AppThinkingToggle           AppKeybinding = "app.thinking.toggle"
	AppSessionToggleNamedFilter AppKeybinding = "app.session.toggleNamedFilter"
	AppEditorExternal           AppKeybinding = "app.editor.external"
	AppMessageCopy              AppKeybinding = "app.message.copy"
	AppMessageFollowUp          AppKeybinding = "app.message.followUp"
	AppMessageDequeue           AppKeybinding = "app.message.dequeue"
	AppClipboardPasteImage      AppKeybinding = "app.clipboard.pasteImage"
	AppSessionNew               AppKeybinding = "app.session.new"
	AppSessionTree              AppKeybinding = "app.session.tree"
	AppSessionFork              AppKeybinding = "app.session.fork"
	AppSessionResume            AppKeybinding = "app.session.resume"
	AppTreeFoldOrUp             AppKeybinding = "app.tree.foldOrUp"
	AppTreeUnfoldOrDown         AppKeybinding = "app.tree.unfoldOrDown"
	AppTreeEditLabel            AppKeybinding = "app.tree.editLabel"
	AppTreeToggleLabelTimestamp AppKeybinding = "app.tree.toggleLabelTimestamp"
	AppSessionTogglePath        AppKeybinding = "app.session.togglePath"
	AppSessionToggleSort        AppKeybinding = "app.session.toggleSort"
	AppSessionRename            AppKeybinding = "app.session.rename"
	AppSessionDelete            AppKeybinding = "app.session.delete"
	AppSessionDeleteNoninvasive AppKeybinding = "app.session.deleteNoninvasive"
	AppModelsSave               AppKeybinding = "app.models.save"
	AppModelsEnableAll          AppKeybinding = "app.models.enableAll"
	AppModelsClearAll           AppKeybinding = "app.models.clearAll"
	AppModelsToggleProvider     AppKeybinding = "app.models.toggleProvider"
	AppModelsReorderUp          AppKeybinding = "app.models.reorderUp"
	AppModelsReorderDown        AppKeybinding = "app.models.reorderDown"
	AppTreeFilterDefault        AppKeybinding = "app.tree.filter.default"
	AppTreeFilterNoTools        AppKeybinding = "app.tree.filter.noTools"
	AppTreeFilterUserOnly       AppKeybinding = "app.tree.filter.userOnly"
	AppTreeFilterLabeledOnly    AppKeybinding = "app.tree.filter.labeledOnly"
	AppTreeFilterAll            AppKeybinding = "app.tree.filter.all"
	AppTreeFilterCycleForward   AppKeybinding = "app.tree.filter.cycleForward"
	AppTreeFilterCycleBackward  AppKeybinding = "app.tree.filter.cycleBackward"
)

// appKeybindingIDs lists every AppKeybinding in the order of upstream's AppKeybindings interface.
var appKeybindingIDs = []AppKeybinding{
	AppInterrupt,
	AppClear,
	AppExit,
	AppSuspend,
	AppThinkingCycle,
	AppThinkingSave,
	AppModelCycleForward,
	AppModelCycleBackward,
	AppModelSelect,
	AppToolsExpand,
	AppThinkingToggle,
	AppSessionToggleNamedFilter,
	AppEditorExternal,
	AppMessageCopy,
	AppMessageFollowUp,
	AppMessageDequeue,
	AppClipboardPasteImage,
	AppSessionNew,
	AppSessionTree,
	AppSessionFork,
	AppSessionResume,
	AppTreeFoldOrUp,
	AppTreeUnfoldOrDown,
	AppTreeEditLabel,
	AppTreeToggleLabelTimestamp,
	AppSessionTogglePath,
	AppSessionToggleSort,
	AppSessionRename,
	AppSessionDelete,
	AppSessionDeleteNoninvasive,
	AppModelsSave,
	AppModelsEnableAll,
	AppModelsClearAll,
	AppModelsToggleProvider,
	AppModelsReorderUp,
	AppModelsReorderDown,
	AppTreeFilterDefault,
	AppTreeFilterNoTools,
	AppTreeFilterUserOnly,
	AppTreeFilterLabeledOnly,
	AppTreeFilterAll,
	AppTreeFilterCycleForward,
	AppTreeFilterCycleBackward,
}
