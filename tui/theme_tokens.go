package tui

// ThemeColor is a foreground theme token: upstream `ThemeColor` (modes/interactive/theme/theme.ts:57).
type ThemeColor string

// The ThemeColor tokens, in upstream order.
const (
	ThemeColorAccent             ThemeColor = "accent"
	ThemeColorBorder             ThemeColor = "border"
	ThemeColorBorderAccent       ThemeColor = "borderAccent"
	ThemeColorBorderMuted        ThemeColor = "borderMuted"
	ThemeColorSuccess            ThemeColor = "success"
	ThemeColorError              ThemeColor = "error"
	ThemeColorWarning            ThemeColor = "warning"
	ThemeColorMuted              ThemeColor = "muted"
	ThemeColorDim                ThemeColor = "dim"
	ThemeColorText               ThemeColor = "text"
	ThemeColorThinkingText       ThemeColor = "thinkingText"
	ThemeColorScrollbarTrack     ThemeColor = "scrollbarTrack"
	ThemeColorScrollbarThumb     ThemeColor = "scrollbarThumb"
	ThemeColorSearchMatchText    ThemeColor = "searchMatchText"
	ThemeColorUserMessageText    ThemeColor = "userMessageText"
	ThemeColorCustomMessageText  ThemeColor = "customMessageText"
	ThemeColorCustomMessageLabel ThemeColor = "customMessageLabel"
	ThemeColorToolTitle          ThemeColor = "toolTitle"
	ThemeColorToolOutput         ThemeColor = "toolOutput"
	ThemeColorMdHeading          ThemeColor = "mdHeading"
	ThemeColorMdLink             ThemeColor = "mdLink"
	ThemeColorMdLinkUrl          ThemeColor = "mdLinkUrl"
	ThemeColorMdCode             ThemeColor = "mdCode"
	ThemeColorMdCodeBlock        ThemeColor = "mdCodeBlock"
	ThemeColorMdCodeBlockBorder  ThemeColor = "mdCodeBlockBorder"
	ThemeColorMdQuote            ThemeColor = "mdQuote"
	ThemeColorMdQuoteBorder      ThemeColor = "mdQuoteBorder"
	ThemeColorMdHr               ThemeColor = "mdHr"
	ThemeColorMdListBullet       ThemeColor = "mdListBullet"
	ThemeColorToolDiffAdded      ThemeColor = "toolDiffAdded"
	ThemeColorToolDiffRemoved    ThemeColor = "toolDiffRemoved"
	ThemeColorToolDiffContext    ThemeColor = "toolDiffContext"
	ThemeColorSyntaxComment      ThemeColor = "syntaxComment"
	ThemeColorSyntaxKeyword      ThemeColor = "syntaxKeyword"
	ThemeColorSyntaxFunction     ThemeColor = "syntaxFunction"
	ThemeColorSyntaxVariable     ThemeColor = "syntaxVariable"
	ThemeColorSyntaxString       ThemeColor = "syntaxString"
	ThemeColorSyntaxNumber       ThemeColor = "syntaxNumber"
	ThemeColorSyntaxType         ThemeColor = "syntaxType"
	ThemeColorSyntaxOperator     ThemeColor = "syntaxOperator"
	ThemeColorSyntaxPunctuation  ThemeColor = "syntaxPunctuation"
	ThemeColorThinkingOff        ThemeColor = "thinkingOff"
	ThemeColorThinkingMinimal    ThemeColor = "thinkingMinimal"
	ThemeColorThinkingLow        ThemeColor = "thinkingLow"
	ThemeColorThinkingMedium     ThemeColor = "thinkingMedium"
	ThemeColorThinkingHigh       ThemeColor = "thinkingHigh"
	ThemeColorThinkingXhigh      ThemeColor = "thinkingXhigh"
	ThemeColorThinkingMax        ThemeColor = "thinkingMax"
	ThemeColorBashMode           ThemeColor = "bashMode"
)

// ThemeBg is a background theme token: upstream `ThemeBg` (theme.ts:108).
type ThemeBg string

// The ThemeBg tokens, in upstream order.
const (
	ThemeBgSelectedBg      ThemeBg = "selectedBg"
	ThemeBgSearchMatchBg   ThemeBg = "searchMatchBg"
	ThemeBgUserMessageBg   ThemeBg = "userMessageBg"
	ThemeBgCustomMessageBg ThemeBg = "customMessageBg"
	ThemeBgToolPendingBg   ThemeBg = "toolPendingBg"
	ThemeBgToolSuccessBg   ThemeBg = "toolSuccessBg"
	ThemeBgToolErrorBg     ThemeBg = "toolErrorBg"
)

// ThemeToken is a foreground or background theme token: upstream `ThemeToken = ThemeColor | ThemeBg` (theme.ts:117).
type ThemeToken string
