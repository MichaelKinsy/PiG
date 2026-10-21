//#region src/index.ts
const CJK = "\\u1100-\\u11ff\\u20a9\\u2329-\\u232a\\u2630-\\u2637\\u268a-\\u268f\\u2e80-\\u2e99\\u2e9b-\\u2ef3\\u2f00-\\u2fd5\\u2ff0-\\u303e\\u3041-\\u3096\\u3099-\\u30ff\\u3105-\\u312f\\u3131-\\u318e\\u3190-\\u31e5\\u31ef-\\u321e\\u3220-\\u3247\\u3250-\\ua48c\\ua490-\\ua4c6\\ua960-\\ua97c\\uac00-\\ud7a3\\ud7b0-\\ud7c6\\ud7cb-\\ud7fb\\uf900-\\ufaff\\ufe10-\\ufe19\\ufe30-\\ufe52\\ufe54-\\ufe66\\ufe68-\\ufe6b\\uff01-\\uffbe\\uffc2-\\uffc7\\uffca-\\uffcf\\uffd2-\\uffd7\\uffda-\\uffdc\\uffe0-\\uffe6\\uffe8-\\uffee\\u{16fe0}-\\u{16fe4}\\u{16ff0}-\\u{16ff6}\\u{17000}-\\u{18cda}\\u{18cff}-\\u{18d20}\\u{18d80}-\\u{18df2}\\u{18e00}-\\u{19191}\\u{191a0}-\\u{191d2}\\u{1aff0}-\\u{1aff3}\\u{1aff5}-\\u{1affb}\\u{1affd}-\\u{1affe}\\u{1b000}-\\u{1b128}\\u{1b132}\\u{1b150}-\\u{1b152}\\u{1b155}\\u{1b164}-\\u{1b168}\\u{1b170}-\\u{1b2fb}\\u{1d300}-\\u{1d356}\\u{1d360}-\\u{1d376}\\u{1f1ae}\\u{1f200}\\u{1f202}\\u{1f210}-\\u{1f219}\\u{1f21b}-\\u{1f22e}\\u{1f230}-\\u{1f231}\\u{1f237}\\u{1f23b}\\u{1f240}-\\u{1f248}\\u{1f260}-\\u{1f265}\\u{1f7da}\\u{20000}-\\u{3fffd}";
const cjkTest = new RegExp(`[${CJK}]`, "u");
const punctuationCjk = new RegExp(`^((?![*_])[\\s\\p{P}\\p{S}${CJK}])`, "u");
function buildRDelimAst(punct, punctSpace, notPunctSpace) {
	return new RegExp(`^[^_*]*?__[^_*]*?\\*[^_*]*?(?=__)|[^*]+(?=[^*])|(?!\\*)${punct}(\\*+)(?=[\\s]|$)|${notPunctSpace}(\\*+)(?!\\*)(?=${punctSpace}|$)|(?!\\*)${punctSpace}(\\*+)(?=${notPunctSpace})|[\\s](\\*+)(?!\\*)(?=${punct})|(?!\\*)${punct}(\\*+)(?!\\*)(?=${punct})|${notPunctSpace}(\\*+)(?=${notPunctSpace})`, "gu");
}
function buildRDelimUnd(punct, punctSpace, notPunctSpace) {
	return new RegExp(`^[^_*]*?\\*\\*[^_*]*?_[^_*]*?(?=\\*\\*)|[^_]+(?=[^_])|(?!_)${punct}(_+)(?=[\\s]|$)|${notPunctSpace}(_+)(?!_)(?=${punctSpace}|$)|(?!_)${punctSpace}(_+)(?=${notPunctSpace})|[\\s](_+)(?!_)(?=${punct})|(?!_)${punct}(_+)(?!_)(?=${punct})`, "gu");
}
function buildRDelimDel(punct, punctSpace, notPunctSpace) {
	return new RegExp(`^[^~]+(?=[^~])|(?!~)${punct}(~~?)(?=[\\s]|$)|${notPunctSpace}(~~?)(?!~)(?=${punctSpace}|$)|(?!~)${punctSpace}(~~?)(?=${notPunctSpace})|[\\s](~~?)(?!~)(?=${punct})|(?!~)${punct}(~~?)(?!~)(?=${punct})|${notPunctSpace}(~~?)(?=${notPunctSpace})`, "gu");
}
const cjkPunct = `[\\p{P}\\p{S}${CJK}]`;
const cjkPunctSpace = `[\\s\\p{P}\\p{S}${CJK}]`;
const cjkNotPunctSpace = `[^\\s\\p{P}\\p{S}${CJK}]`;
const cjkDelPunct = `(?![*_])[\\p{P}\\p{S}${CJK}]`;
const cjkDelPunctSpace = `(?![*_])[\\s\\p{P}\\p{S}${CJK}]`;
const cjkDelNotPunctSpace = `(?:[^\\s\\p{P}\\p{S}${CJK}]|[*_])`;
const cjkPunctGfm = `(?!~)[\\p{P}\\p{S}${CJK}]`;
const cjkPunctSpaceGfm = `(?!~)[\\s\\p{P}\\p{S}${CJK}]`;
const cjkNotPunctSpaceGfm = `(?:[^\\s\\p{P}\\p{S}${CJK}]|~)`;
const emStrongRDelimAstCjk = buildRDelimAst(cjkPunct, cjkPunctSpace, cjkNotPunctSpace);
const emStrongRDelimAstCjkGfm = buildRDelimAst(cjkPunctGfm, cjkPunctSpaceGfm, cjkNotPunctSpaceGfm);
const emStrongRDelimUndCjk = buildRDelimUnd(cjkPunct, cjkPunctSpace, cjkNotPunctSpace);
const delRDelimCjk = buildRDelimDel(cjkDelPunct, cjkDelPunctSpace, cjkDelNotPunctSpace);
/**
* Checks whether the character immediately before `src` should be treated as CJK.
*
* Marked passes `prevChar` as a single UTF-16 code unit, so supplementary-plane
* characters may arrive as a lone surrogate and variation sequences may point at
* the variation selector instead of the base character. Recover from `maskedSrc`
* when needed so CJK-aware flanking checks see the actual preceding code point.
*/
function isPrevCharCjk(prevChar, maskedSrc, src) {
	let prevIsCjk = punctuationCjk.test(prevChar);
	if (!prevIsCjk && prevChar) {
		const prevIdx = maskedSrc.length - src.length;
		if (prevIdx >= 1) {
			let idx = prevIdx - 1;
			let code = maskedSrc.charCodeAt(idx);
			if (code >= 65024 && code <= 65038 && idx >= 1) {
				idx--;
				code = maskedSrc.charCodeAt(idx);
			}
			if ((code & 64512) === 56320 && idx >= 1) {
				const cp = maskedSrc.codePointAt(idx - 1);
				if (cp !== void 0 && cp > 65535) {
					if (cp >= 917760 && cp <= 917999) prevIsCjk = true;
					else prevIsCjk = cjkTest.test(String.fromCodePoint(cp));
				}
			} else prevIsCjk = cjkTest.test(String.fromCharCode(code));
		}
	}
	return prevIsCjk;
}
/**
* Checks whether a matched right delimiter is immediately adjacent to CJK text.
*
* Marked classifies these matches as left-only, right-only, or both using regex
* groups. When CJK sits next to the delimiter, we treat the delimiter as "both"
* so GFM emphasis/strikethrough can close across CJK punctuation boundaries.
*/
function isCjkAdjacentToDelimiter(rightMatch, clippedMaskedSrc) {
	if (!(rightMatch[1] || rightMatch[2] || rightMatch[3] || rightMatch[4])) return false;
	const charBefore = String.fromCodePoint(rightMatch[0].codePointAt(0));
	const afterPos = rightMatch.index + rightMatch[0].length;
	const charAfter = afterPos < clippedMaskedSrc.length ? String.fromCodePoint(clippedMaskedSrc.codePointAt(afterPos)) : "";
	return cjkTest.test(charBefore) || cjkTest.test(charAfter);
}
function usesModernEmStrongLayout(match) {
	return match.length >= 5;
}
function hasEmStrongLeadContent(match) {
	return Boolean(match[1] || match[2] || match[3] || match[4]);
}
function isEmStrongUnderscoreWordChar(match) {
	return usesModernEmStrongLayout(match) ? Boolean(match[4]) : Boolean(match[3]);
}
function getEmStrongOpeningPunctuation(match) {
	if (usesModernEmStrongLayout(match)) return match[1] || match[3] || "";
	return match[1] || match[2] || "";
}
/**
* Creates a marked extension that makes emphasis markers CJK-friendly.
*
* This extension modifies how `*` and `_` emphasis delimiters are detected
* so that they work correctly adjacent to CJK (Chinese, Japanese, Korean) characters.
*
* In standard CommonMark, emphasis delimiters like `**` may not be recognized
* when adjacent to CJK punctuation, because CJK characters are not classified
* as Unicode punctuation or whitespace. This extension treats CJK characters
* as equivalent to punctuation for flanking delimiter detection.
*/
function markedCjkFriendly() {
	return { tokenizer: {
		emStrong(src, maskedSrc, prevChar = "") {
			const { rules } = this;
			const match = rules.inline.emStrongLDelim.exec(src);
			if (!match) return false;
			const modernEmStrongLayout = usesModernEmStrongLayout(match);
			if (modernEmStrongLayout && !hasEmStrongLeadContent(match)) return;
			if (isEmStrongUnderscoreWordChar(match) && prevChar.match(rules.other.unicodeAlphaNumeric)) return false;
			const nextChar = getEmStrongOpeningPunctuation(match);
			const prevIsCjk = isPrevCharCjk(prevChar, maskedSrc, src);
			if (!nextChar || !prevChar || rules.inline.punctuation.exec(prevChar) || prevIsCjk || cjkTest.test(nextChar)) {
				const lLength = [...match[0]].length - 1;
				let rDelim;
				let rLength;
				let delimTotal = lLength;
				let midDelimTotal = 0;
				const prevCharIsSameDelimiter = modernEmStrongLayout && prevChar === match[0][0];
				const isGfm = rules.inline.emStrongRDelimAst.source.includes("(?!~)");
				let endReg;
				if (match[0][0] === "*") endReg = isGfm ? emStrongRDelimAstCjkGfm : emStrongRDelimAstCjk;
				else endReg = emStrongRDelimUndCjk;
				endReg.lastIndex = 0;
				const clippedMaskedSrc = maskedSrc.slice(-1 * src.length + lLength);
				let rMatch;
				while ((rMatch = endReg.exec(clippedMaskedSrc)) != null) {
					rDelim = rMatch[1] || rMatch[2] || rMatch[3] || rMatch[4] || rMatch[5] || rMatch[6];
					if (!rDelim) continue;
					rLength = [...rDelim].length;
					const isCjkAdjacent = isCjkAdjacentToDelimiter(rMatch, clippedMaskedSrc);
					const isLeftOnly = Boolean(rMatch[3] || rMatch[4]);
					const isBoth = Boolean(rMatch[5] || rMatch[6]);
					if (isLeftOnly && !isCjkAdjacent) {
						delimTotal += rLength;
						continue;
					}
					if (isBoth || isCjkAdjacent) {
						if (lLength % 3 && !((lLength + rLength) % 3)) {
							midDelimTotal += rLength;
							continue;
						}
						if (prevCharIsSameDelimiter && isBoth) break;
					}
					delimTotal -= rLength;
					if (delimTotal > 0) continue;
					rLength = Math.min(rLength, rLength + delimTotal + midDelimTotal);
					const lastCharLength = rMatch[0].codePointAt(0) > 65535 ? 2 : 1;
					const raw = src.slice(0, lLength + rMatch.index + lastCharLength + rLength);
					if (Math.min(lLength, rLength) % 2) {
						const text = raw.slice(1, -1);
						return {
							type: "em",
							raw,
							text,
							tokens: this.lexer.inlineTokens(text)
						};
					}
					const text = raw.slice(2, -2);
					return {
						type: "strong",
						raw,
						text,
						tokens: this.lexer.inlineTokens(text)
					};
				}
			}
		},
		del(src, maskedSrc, prevChar = "") {
			const { rules } = this;
			const match = rules.inline.delLDelim.exec(src);
			if (!match) return false;
			const nextChar = match[1] || "";
			const prevIsCjk = isPrevCharCjk(prevChar, maskedSrc, src);
			if (!nextChar || !prevChar || rules.inline.punctuation.exec(prevChar) || prevIsCjk || cjkTest.test(nextChar)) {
				const lLength = [...match[0]].length - 1;
				let rDelim;
				let rLength;
				let delimTotal = lLength;
				delRDelimCjk.lastIndex = 0;
				const clippedMaskedSrc = maskedSrc.slice(-1 * src.length + lLength);
				let rMatch;
				while ((rMatch = delRDelimCjk.exec(clippedMaskedSrc)) != null) {
					rDelim = rMatch[1] || rMatch[2] || rMatch[3] || rMatch[4] || rMatch[5] || rMatch[6];
					if (!rDelim) continue;
					rLength = [...rDelim].length;
					if (rLength !== lLength) continue;
					const isCjkAdjacent = isCjkAdjacentToDelimiter(rMatch, clippedMaskedSrc);
					if ((rMatch[3] || rMatch[4]) && !isCjkAdjacent) {
						delimTotal += rLength;
						continue;
					}
					delimTotal -= rLength;
					if (delimTotal > 0) continue;
					rLength = Math.min(rLength, rLength + delimTotal);
					const lastCharLength = rMatch[0].codePointAt(0) > 65535 ? 2 : 1;
					const raw = src.slice(0, lLength + rMatch.index + lastCharLength + rLength);
					const text = raw.slice(lLength, -lLength);
					return {
						type: "del",
						raw,
						text,
						tokens: this.lexer.inlineTokens(text)
					};
				}
			}
		}
	} };
}
//#endregion
export { markedCjkFriendly as default };
