package ecmaregex

import (
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

// Data source: Go's unicode tables (unicode.Version) supply General_Category, Script and every binary property that is a table or a derivation of tables there. Script_Extensions and the binary properties Go has no data for (Bidi_Mirrored, Case_Ignorable, Changes_When_*, Emoji*, Extended_Pictographic, XID_*) come from property_data.go, which gen/property_data.mjs generates from the Unicode tables of the Node release the golden files use (Unicode 17.0). Where the Go toolchain is on an older Unicode version, sets built from Go's tables lag the generated ones by the characters assigned in between.

// tablesBuilt counts the lookup tables built so far. A table is built the first time a pattern needs it, so that importing the package builds none.
var tablesBuilt atomic.Int32

// lazyTable returns a function that builds the table with build on its first call and returns the same table every time.
func lazyTable[T any](build func() T) func() T {
	return sync.OnceValue(func() T {
		tablesBuilt.Add(1)
		return build()
	})
}

// propertyRanges resolves the text between the braces of \p{...}: General_Category=Lu, Script=Latin, a binary property name, or a lone General_Category value. It reports false for a name or value ECMA-262 does not define.
func propertyRanges(expr string) (runeSet, bool) {
	cached, ok := propertyCache.Load(expr)
	if ok {
		return cached.(runeSet), true
	}
	set, ok := resolveProperty(expr)
	if ok {
		propertyCache.Store(expr, set)
	}
	return set, ok
}

var propertyCache sync.Map

func resolveProperty(expr string) (runeSet, bool) {
	name, value, hasValue := strings.Cut(expr, "=")
	if !hasValue {
		if set, ok := generalCategory(name); ok {
			return set, true
		}
		return binaryProperty(name)
	}
	switch name {
	case "General_Category", "gc":
		return generalCategory(value)
	case "Script", "sc":
		return script(value)
	case "Script_Extensions", "scx":
		return scriptExtensions(value)
	}
	return nil, false
}

// rangeTable converts a Go table into a set. Go stores alternating code points as strided ranges, which become one range per code point here.
func rangeTable(table *unicode.RangeTable) runeSet {
	var out []runeRange
	for _, r := range table.R16 {
		out = appendStrided(out, rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	for _, r := range table.R32 {
		out = appendStrided(out, rune(r.Lo), rune(r.Hi), rune(r.Stride))
	}
	return newRuneSet(out)
}

func appendStrided(out []runeRange, lo, hi, stride rune) []runeRange {
	if stride == 1 {
		return append(out, runeRange{lo, hi})
	}
	for c := lo; c <= hi; c += stride {
		out = append(out, runeRange{c, c})
	}
	return out
}

var (
	categoriesOnce sync.Once
	categorySets   map[string]runeSet
)

// minorCategories are the two-letter General_Category values that Go's unicode.Categories holds; the unassigned code points (Cn) are the ones outside all of them.
var minorCategories = []string{"Lu", "Ll", "Lt", "Lm", "Lo", "Mn", "Mc", "Me", "Nd", "Nl", "No", "Pc", "Pd", "Ps", "Pe", "Pi", "Pf", "Po", "Sm", "Sc", "Sk", "So", "Zs", "Zl", "Zp", "Cc", "Cf", "Co", "Cs"}

// categoryNames maps every General_Category name of ECMA-262 (PropertyValueAliases.txt) to its short name.
var categoryNames = lazyTable(func() map[string]string {
	return map[string]string{
		"Other": "C", "Control": "Cc", "cntrl": "Cc", "Format": "Cf", "Unassigned": "Cn", "Private_Use": "Co", "Surrogate": "Cs",
		"Letter": "L", "Cased_Letter": "LC", "Lowercase_Letter": "Ll", "Modifier_Letter": "Lm", "Other_Letter": "Lo",
		"Titlecase_Letter": "Lt", "Uppercase_Letter": "Lu", "Mark": "M", "Combining_Mark": "M", "Spacing_Mark": "Mc",
		"Enclosing_Mark": "Me", "Nonspacing_Mark": "Mn", "Number": "N", "Decimal_Number": "Nd", "digit": "Nd",
		"Letter_Number": "Nl", "Other_Number": "No", "Punctuation": "P", "punct": "P", "Connector_Punctuation": "Pc",
		"Dash_Punctuation": "Pd", "Close_Punctuation": "Pe", "Final_Punctuation": "Pf", "Initial_Punctuation": "Pi",
		"Other_Punctuation": "Po", "Open_Punctuation": "Ps", "Symbol": "S", "Currency_Symbol": "Sc", "Modifier_Symbol": "Sk",
		"Math_Symbol": "Sm", "Other_Symbol": "So", "Separator": "Z", "Line_Separator": "Zl", "Paragraph_Separator": "Zp",
		"Space_Separator": "Zs",
	}
})

func buildCategories() {
	categorySets = map[string]runeSet{}
	var assigned runeSet
	for _, name := range minorCategories {
		categorySets[name] = rangeTable(unicode.Categories[name])
		assigned = assigned.union(categorySets[name])
	}
	categorySets["Cn"] = assigned.complement(maxCodePoint)
	for major, minors := range map[string][]string{
		"L": {"Lu", "Ll", "Lt", "Lm", "Lo"}, "LC": {"Lu", "Ll", "Lt"}, "M": {"Mn", "Mc", "Me"}, "N": {"Nd", "Nl", "No"},
		"P": {"Pc", "Pd", "Ps", "Pe", "Pi", "Pf", "Po"}, "S": {"Sm", "Sc", "Sk", "So"}, "Z": {"Zs", "Zl", "Zp"},
		"C": {"Cc", "Cf", "Co", "Cs", "Cn"},
	} {
		var members []runeSet
		for _, minor := range minors {
			members = append(members, categorySets[minor])
		}
		categorySets[major] = runeSet(nil).union(members...)
	}
}

func generalCategory(name string) (runeSet, bool) {
	categoriesOnce.Do(buildCategories)
	if set, ok := categorySets[name]; ok {
		return set, true
	}
	if short, ok := categoryNames()[name]; ok {
		return categorySets[short], true
	}
	return nil, false
}

var (
	scriptsOnce sync.Once
	scriptSets  map[string]runeSet
)

func buildScripts() {
	scriptSets = make(map[string]runeSet, len(unicode.Scripts)+1)
	var known runeSet
	for name, table := range unicode.Scripts {
		scriptSets[name] = rangeTable(table)
		known = known.union(scriptSets[name])
	}
	scriptSets["Unknown"] = known.complement(maxCodePoint)
}

// scriptName maps a Script value (a long name or a short alias) to the long name, or "" when V8 does not accept it.
func scriptName(value string) string {
	scriptsOnce.Do(buildScripts)
	if _, ok := scriptSets[value]; ok {
		return value
	}
	if long, ok := scriptAliases()[value]; ok {
		return long
	}
	return ""
}

func script(value string) (runeSet, bool) {
	name := scriptName(value)
	if name == "" {
		return nil, false
	}
	return scriptSets[name], true
}

func scriptExtensions(value string) (runeSet, bool) {
	name := scriptName(value)
	if name == "" {
		return nil, false
	}
	set := scriptSets[name]
	if removed, ok := scriptExtensionsRemoved()[name]; ok {
		set = set.minus(parseRangeList(removed))
	}
	if added, ok := scriptExtensionsAdded()[name]; ok {
		set = set.union(parseRangeList(added))
	}
	return set, true
}

// binaryPropertyNames maps every binary property name and alias of ECMA-262 to the long name.
var binaryPropertyNames = lazyTable(func() map[string]string {
	return map[string]string{
		"ASCII": "ASCII", "ASCII_Hex_Digit": "ASCII_Hex_Digit", "AHex": "ASCII_Hex_Digit", "Alphabetic": "Alphabetic",
		"Alpha": "Alphabetic", "Any": "Any", "Assigned": "Assigned", "Bidi_Control": "Bidi_Control", "Bidi_C": "Bidi_Control",
		"Bidi_Mirrored": "Bidi_Mirrored", "Bidi_M": "Bidi_Mirrored", "Case_Ignorable": "Case_Ignorable", "CI": "Case_Ignorable",
		"Cased": "Cased", "Changes_When_Casefolded": "Changes_When_Casefolded", "CWCF": "Changes_When_Casefolded",
		"Changes_When_Casemapped": "Changes_When_Casemapped", "CWCM": "Changes_When_Casemapped",
		"Changes_When_Lowercased": "Changes_When_Lowercased", "CWL": "Changes_When_Lowercased",
		"Changes_When_NFKC_Casefolded": "Changes_When_NFKC_Casefolded", "CWKCF": "Changes_When_NFKC_Casefolded",
		"Changes_When_Titlecased": "Changes_When_Titlecased", "CWT": "Changes_When_Titlecased",
		"Changes_When_Uppercased": "Changes_When_Uppercased", "CWU": "Changes_When_Uppercased", "Dash": "Dash",
		"Default_Ignorable_Code_Point": "Default_Ignorable_Code_Point", "DI": "Default_Ignorable_Code_Point",
		"Deprecated": "Deprecated", "Dep": "Deprecated", "Diacritic": "Diacritic", "Dia": "Diacritic", "Emoji": "Emoji",
		"Emoji_Component": "Emoji_Component", "EComp": "Emoji_Component", "Emoji_Modifier": "Emoji_Modifier",
		"EMod": "Emoji_Modifier", "Emoji_Modifier_Base": "Emoji_Modifier_Base", "EBase": "Emoji_Modifier_Base",
		"Emoji_Presentation": "Emoji_Presentation", "EPres": "Emoji_Presentation", "Extended_Pictographic": "Extended_Pictographic",
		"ExtPict": "Extended_Pictographic", "Extender": "Extender", "Ext": "Extender", "Grapheme_Base": "Grapheme_Base",
		"Gr_Base": "Grapheme_Base", "Grapheme_Extend": "Grapheme_Extend", "Gr_Ext": "Grapheme_Extend", "Hex_Digit": "Hex_Digit",
		"Hex": "Hex_Digit", "IDS_Binary_Operator": "IDS_Binary_Operator", "IDSB": "IDS_Binary_Operator",
		"IDS_Trinary_Operator": "IDS_Trinary_Operator", "IDST": "IDS_Trinary_Operator", "ID_Continue": "ID_Continue",
		"IDC": "ID_Continue", "ID_Start": "ID_Start", "IDS": "ID_Start", "Ideographic": "Ideographic", "Ideo": "Ideographic",
		"Join_Control": "Join_Control", "Join_C": "Join_Control", "Logical_Order_Exception": "Logical_Order_Exception",
		"LOE": "Logical_Order_Exception", "Lowercase": "Lowercase", "Lower": "Lowercase", "Math": "Math",
		"Noncharacter_Code_Point": "Noncharacter_Code_Point", "NChar": "Noncharacter_Code_Point",
		"Pattern_Syntax": "Pattern_Syntax", "Pat_Syn": "Pattern_Syntax", "Pattern_White_Space": "Pattern_White_Space",
		"Pat_WS": "Pattern_White_Space", "Quotation_Mark": "Quotation_Mark", "QMark": "Quotation_Mark", "Radical": "Radical",
		"Regional_Indicator": "Regional_Indicator", "RI": "Regional_Indicator", "Sentence_Terminal": "Sentence_Terminal",
		"STerm": "Sentence_Terminal", "Soft_Dotted": "Soft_Dotted", "SD": "Soft_Dotted", "Terminal_Punctuation": "Terminal_Punctuation",
		"Term": "Terminal_Punctuation", "Unified_Ideograph": "Unified_Ideograph", "UIdeo": "Unified_Ideograph",
		"Uppercase": "Uppercase", "Upper": "Uppercase", "Variation_Selector": "Variation_Selector", "VS": "Variation_Selector",
		"White_Space": "White_Space", "space": "White_Space", "XID_Continue": "XID_Continue", "XIDC": "XID_Continue",
		"XID_Start": "XID_Start", "XIDS": "XID_Start",
	}
})

func binaryProperty(name string) (runeSet, bool) {
	long, ok := binaryPropertyNames()[name]
	if !ok {
		return nil, false
	}
	return longBinaryProperty(long), true
}

// goProperty is the set of a table of unicode.Properties, which names the property exactly as ECMA-262 does.
func goProperty(name string) runeSet {
	table := unicode.Properties[name]
	if table == nil {
		return nil
	}
	return rangeTable(table)
}

func category(name string) runeSet {
	categoriesOnce.Do(buildCategories)
	return categorySets[name]
}

// longBinaryProperty builds a binary property by its long name. Each derivation is the definition of DerivedCoreProperties.txt.
func longBinaryProperty(name string) runeSet {
	switch name {
	case "ASCII":
		return runeSet{{0, 0x7F}}
	case "Any":
		return runeSet{{0, maxCodePoint}}
	case "Assigned":
		return category("Cn").complement(maxCodePoint)
	case "ASCII_Hex_Digit", "Bidi_Control", "Dash", "Deprecated", "Diacritic", "Extender", "Hex_Digit", "IDS_Binary_Operator",
		"IDS_Trinary_Operator", "Ideographic", "Join_Control", "Logical_Order_Exception", "Noncharacter_Code_Point",
		"Pattern_Syntax", "Pattern_White_Space", "Quotation_Mark", "Radical", "Regional_Indicator", "Sentence_Terminal",
		"Soft_Dotted", "Terminal_Punctuation", "Unified_Ideograph", "Variation_Selector", "White_Space":
		return goProperty(name)
	case "Lowercase":
		return category("Ll").union(goProperty("Other_Lowercase"))
	case "Uppercase":
		return category("Lu").union(goProperty("Other_Uppercase"))
	case "Cased":
		return category("Lu").union(category("Ll"), category("Lt"), goProperty("Other_Lowercase"), goProperty("Other_Uppercase"))
	case "Alphabetic":
		return category("L").union(category("Nl"), goProperty("Other_Alphabetic"), goProperty("Other_Lowercase"), goProperty("Other_Uppercase"))
	case "Math":
		return category("Sm").union(goProperty("Other_Math"))
	case "Grapheme_Extend":
		return category("Me").union(category("Mn"), goProperty("Other_Grapheme_Extend"))
	case "Grapheme_Base":
		return runeSet{{0, maxCodePoint}}.minus(category("Cc"), category("Cf"), category("Cs"), category("Co"), category("Cn"),
			category("Zl"), category("Zp"), longBinaryProperty("Grapheme_Extend"))
	case "Default_Ignorable_Code_Point":
		return goProperty("Other_Default_Ignorable_Code_Point").union(category("Cf"), goProperty("Variation_Selector")).
			minus(goProperty("White_Space"), runeSet{{0xFFF9, 0xFFFB}, {0x13430, 0x13440}}, goProperty("Prepended_Concatenation_Mark"))
	case "ID_Start":
		return idStartSet()
	case "ID_Continue":
		return idContinueSet()
	}
	return parseRangeList(generatedProperties()[name])
}

func idStartSet() runeSet {
	return category("L").union(category("Nl"), goProperty("Other_ID_Start")).minus(goProperty("Pattern_Syntax"), goProperty("Pattern_White_Space"))
}

func idContinueSet() runeSet {
	return idStartSet().union(category("Mn"), category("Mc"), category("Nd"), category("Pc"), goProperty("Other_ID_Continue")).
		minus(goProperty("Pattern_Syntax"), goProperty("Pattern_White_Space"))
}
