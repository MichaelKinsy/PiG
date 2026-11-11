// The property names of ECMA-262 (table of binary properties, General_Category values) and the script names V8 accepts.
import { readFileSync } from "node:fs";
import { rangesOf, sameRanges } from "./unicode.mjs";

// Each entry lists the names of one General_Category value (the first is the short name).
export const generalCategories = [
  ["C", "Other"], ["Cc", "Control", "cntrl"], ["Cf", "Format"], ["Cn", "Unassigned"], ["Co", "Private_Use"], ["Cs", "Surrogate"],
  ["L", "Letter"], ["LC", "Cased_Letter"], ["Ll", "Lowercase_Letter"], ["Lm", "Modifier_Letter"], ["Lo", "Other_Letter"],
  ["Lt", "Titlecase_Letter"], ["Lu", "Uppercase_Letter"],
  ["M", "Mark", "Combining_Mark"], ["Mc", "Spacing_Mark"], ["Me", "Enclosing_Mark"], ["Mn", "Nonspacing_Mark"],
  ["N", "Number"], ["Nd", "Decimal_Number", "digit"], ["Nl", "Letter_Number"], ["No", "Other_Number"],
  ["P", "Punctuation", "punct"], ["Pc", "Connector_Punctuation"], ["Pd", "Dash_Punctuation"], ["Pe", "Close_Punctuation"],
  ["Pf", "Final_Punctuation"], ["Pi", "Initial_Punctuation"], ["Po", "Other_Punctuation"], ["Ps", "Open_Punctuation"],
  ["S", "Symbol"], ["Sc", "Currency_Symbol"], ["Sk", "Modifier_Symbol"], ["Sm", "Math_Symbol"], ["So", "Other_Symbol"],
  ["Z", "Separator"], ["Zl", "Line_Separator"], ["Zp", "Paragraph_Separator"], ["Zs", "Space_Separator"],
];

// Each entry lists the names of one binary property (the first is the long name).
export const binaryProperties = [
  ["ASCII"], ["ASCII_Hex_Digit", "AHex"], ["Alphabetic", "Alpha"], ["Any"], ["Assigned"], ["Bidi_Control", "Bidi_C"],
  ["Bidi_Mirrored", "Bidi_M"], ["Case_Ignorable", "CI"], ["Cased"], ["Changes_When_Casefolded", "CWCF"],
  ["Changes_When_Casemapped", "CWCM"], ["Changes_When_Lowercased", "CWL"], ["Changes_When_NFKC_Casefolded", "CWKCF"],
  ["Changes_When_Titlecased", "CWT"], ["Changes_When_Uppercased", "CWU"], ["Dash"], ["Default_Ignorable_Code_Point", "DI"],
  ["Deprecated", "Dep"], ["Diacritic", "Dia"], ["Emoji"], ["Emoji_Component", "EComp"], ["Emoji_Modifier", "EMod"],
  ["Emoji_Modifier_Base", "EBase"], ["Emoji_Presentation", "EPres"], ["Extended_Pictographic", "ExtPict"], ["Extender", "Ext"],
  ["Grapheme_Base", "Gr_Base"], ["Grapheme_Extend", "Gr_Ext"], ["Hex_Digit", "Hex"], ["IDS_Binary_Operator", "IDSB"],
  ["IDS_Trinary_Operator", "IDST"], ["ID_Continue", "IDC"], ["ID_Start", "IDS"], ["Ideographic", "Ideo"],
  ["Join_Control", "Join_C"], ["Logical_Order_Exception", "LOE"], ["Lowercase", "Lower"], ["Math"],
  ["Noncharacter_Code_Point", "NChar"], ["Pattern_Syntax", "Pat_Syn"], ["Pattern_White_Space", "Pat_WS"],
  ["Quotation_Mark", "QMark"], ["Radical"], ["Regional_Indicator", "RI"], ["Sentence_Terminal", "STerm"],
  ["Soft_Dotted", "SD"], ["Terminal_Punctuation", "Term"], ["Unified_Ideograph", "UIdeo"], ["Uppercase", "Upper"],
  ["Variation_Selector", "VS"], ["White_Space", "space"], ["XID_Continue", "XIDC"], ["XID_Start", "XIDS"],
];

// scriptLongNames are the names of Go's unicode.Scripts plus the two names V8 accepts that Go has no table for.
export const scriptLongNames = [
  ...readFileSync(new URL("./script_names.txt", import.meta.url), "utf8").split("\n").filter(Boolean),
  "Unknown",
  "Katakana_Or_Hiragana",
];

// discoverScriptAliases tries every four-letter title-case code and maps each one V8 accepts to the long script name with the same code point set.
export function discoverScriptAliases() {
  const longRanges = new Map();
  for (const name of scriptLongNames) {
    const r = rangesOf(`Script=${name}`);
    if (r) longRanges.set(name, r);
  }
  const aliases = new Map();
  const A = "A".charCodeAt(0);
  const a = "a".charCodeAt(0);
  for (let i = 0; i < 26 ** 4; i++) {
    const code = String.fromCharCode(A + (i % 26), a + (Math.floor(i / 26) % 26), a + (Math.floor(i / 676) % 26), a + Math.floor(i / 17576));
    try {
      new RegExp(`\\p{Script=${code}}`, "u");
    } catch {
      continue;
    }
    const r = rangesOf(`Script=${code}`);
    const target = [...longRanges].filter(([, lr]) => sameRanges(lr, r)).map(([n]) => n);
    if (target.length !== 1) throw new Error(`script code ${code} maps to ${target.length} long names`);
    aliases.set(code, target[0]);
  }
  return { aliases, longRanges };
}
