// Generates surfaces_locale_tables.go from the running Node's Intl (ICU/CLDR): `node testdata/locale_tables.mjs | gofmt > surfaces_locale_tables.go`
// (go generate, in the oracle container). Regenerate when the pinned Node changes; TestLocaleTablesMatchIntl checks the Go side
// against the same Intl over a corpus, so a stale table fails there.
import { readFileSync } from "node:fs";
import process from "node:process";

const letters = "abcdefghijklmnopqrstuvwxyz";
const alpha = (n) => {
	let out = [""];
	for (let i = 0; i < n; i++) out = out.flatMap((p) => [...letters].map((c) => p + c));
	return out;
};
const canon = (tag) => {
	try {
		return Intl.getCanonicalLocales(tag)[0];
	} catch {
		return undefined;
	}
};
const languages = [...alpha(2), ...alpha(3)];
const regions = [...alpha(2).map((r) => r.toUpperCase()), ...Array.from({ length: 1000 }, (_, i) => String(i).padStart(3, "0"))];

// Language aliases: the canonical form of a bare language (it may add a script or a region: sh is sr-Latn).
const langAlias = {};
for (const l of languages) {
	const c = canon(l);
	if (c !== undefined && c !== l) langAlias[l] = c;
}

// Region aliases: the default replacement (with und), and the languages that get another (a region alias with several
// replacements picks by the language's likely region).
const regionAlias = {};
const regionByLang = {};
for (const r of regions) {
	const c = canon("und-" + r);
	if (c === undefined || c === "und-" + r) continue;
	const dflt = c.slice(4);
	regionAlias[r] = dflt;
	for (const l of languages) {
		const own = canon(l + "-" + r);
		if (own === undefined) continue;
		const base = langAlias[l] ?? l;
		const want = own.startsWith(base + "-") ? own.slice(base.length + 1) : undefined;
		if (want !== undefined && want !== dflt) (regionByLang[r] ??= {})[l] = want;
	}
}

// Languages that Intl aliases only when the tag is not all lower case: `TW` and `Tw` are `ak`, `tw` stays `tw` (an alias of a bare language
// that has no other subtag is left alone when it is spelled in lower case). Every spelling but all lower case is asked.
const caseVariants = (language) => {
	let out = [""];
	for (const c of language) out = out.flatMap((p) => [p + c, p + c.toUpperCase()]);
	return out.filter((v) => v !== language);
};
const nonLowercaseLangAlias = {};
for (const l of languages) {
	if (langAlias[l] !== undefined) continue;
	const c = canon(l.toUpperCase());
	if (c === undefined || c.toLowerCase() === l) continue;
	nonLowercaseLangAlias[l] = c;
	for (const spelling of caseVariants(l)) if (canon(spelling) !== c) throw new Error(`${spelling} is ${canon(spelling)}, ${l.toUpperCase()} is ${c}`);
}
// The source language of a `-t-` extension is aliased the same way, in lower case too (`en-t-tw` is `en-t-ak`, `en-t-tnf-latn` is
// `en-t-fa-latn-af`): this asks every language and throws for one that is neither a language alias nor in the table above.
for (const l of languages) {
	const c = canon("en-t-" + l);
	// `yes` is `true`, a value that is dropped: `en-t-yes` is `en-t`.
	if (c === undefined || c === "en-t") continue;
	const want = (langAlias[l] ?? nonLowercaseLangAlias[l] ?? l).toLowerCase();
	if (c.slice("en-t-".length) !== want) throw new Error(`en-t-${l} is ${c}, not en-t-${want}`);
}

// Value aliases of `-u-` keywords and `-t-` fields. Node's ICU (CLDR bcp47 data) has no list to read, so each (key, value) is asked of
// Intl: every key from `0a` to `9z` and `aa` to `zz` (a letter or a digit, then a letter) with `yes`, the values of CLDR's own lists (Intl.supportedValuesOf, and
// the names of the data files that the aliases come from), and every value of three characters; then, for the keys that showed an alias, every
// value of four characters, and of five letters for the keys of regions and time zones. A `true` alias is the value dropped (`yes` is `true`
// for every key).
const alnum = letters + "0123456789";
const lettersOf = (n) => (n === 0 ? [""] : lettersOf(n - 1).flatMap((p) => [...letters].map((c) => p + c)));
const triples = [];
for (const a of alnum) for (const b of alnum) for (const c of alnum) triples.push(a + b + c);
const quads = triples.flatMap((t) => [...alnum].map((c) => t + c));
const unicodeKeys = [...alnum].flatMap((a) => [...letters].map((b) => a + b));
const unicodeAlias = {};
const unicodeAliasKeys = new Set();
const askUnicode = (key, value) => {
	const c = canon("en-u-" + key + "-" + value);
	if (c === undefined) return;
	const prefix = "en-u-" + key;
	if (c === prefix + "-" + value || value === "yes" || value === "true") return;
	if (!c.startsWith(prefix + "-")) return;
	unicodeAlias[key + "-" + value] = c.slice(prefix.length + 1);
	unicodeAliasKeys.add(key);
};
// `yes` is `true` (dropped) for every key, or this generator's `yesIsTrue` does not hold.
for (const key of unicodeKeys) {
	if (canon("en-u-" + key + "-yes") !== "en-u-" + key) throw new Error(`en-u-${key}-yes is not en-u-${key}`);
}
const supported = (what) => {
	try {
		return Intl.supportedValuesOf(what);
	} catch {
		return [];
	}
};
const words = [
	...supported("calendar"), ...supported("collation"), ...supported("numberingSystem"), ...supported("currency").map((c) => c.toLowerCase()),
	"islamicc", "ethiopic-amete-alem", "islamic-civil", "gregorian", "iso8601", "traditional", "dictionary", "phonebook", "gb2312han", "big5han",
	"primary", "secondary", "tertiary", "quaternary", "quarternary", "identical", "imperial", "metric", "uksystem", "ussystem", "level1", "level2", "level3",
	"level4", "identic", "true", "false",
];
for (const key of unicodeKeys) for (const word of words) askUnicode(key, word);
// The time zone aliases of six letters and more are the names of IANA's zones and links (`Hongkong` is `hkhkg`; automation/gen/zone-links.txt and
// the zones Intl lists, as one word, the last segment, and the segments joined: `usnavajo`), `factory`, and every six letters that begin with a
// region (`mxstis`).
const zoneWords = new Set(["factory"]);
const zoneNames = [...readFileSync(new URL("../../../../automation/gen/zone-links.txt", import.meta.url), "utf8").split("\n"), ...supported("timeZone")];
for (const name of zoneNames) {
	const parts = name.trim().toLowerCase().split("/").map((part) => part.replace(/[^a-z0-9]/g, ""));
	for (const word of [parts.join(""), parts[parts.length - 1]]) if (word.length >= 3 && word.length <= 8) zoneWords.add(word);
	// IANA's `backward` file links the zones of a country's folder under the country's name (`US/Navajo`): the folders' names before each.
	for (const folder of ["us", "canada", "brazil", "chile", "mexico", "etc"]) {
		const word = folder + parts[parts.length - 1];
		if (word.length <= 8) zoneWords.add(word);
	}
}
const tzKeys = ["tz", "rg", "sd"];
const fewKeys = ["ca", "co", "ks", "ms", "nu", ...tzKeys];
for (const key of fewKeys) for (const t of triples) askUnicode(key, t);
for (const key of unicodeKeys) if (!fewKeys.includes(key)) for (const t of triples) askUnicode(key, t);
for (const key of unicodeAliasKeys) for (const q4 of quads) askUnicode(key, q4);
for (const key of tzKeys) for (const q4 of quads) askUnicode(key, q4);
for (const word of zoneWords) askUnicode("tz", word);
const regionNames = new Intl.DisplayNames("en", { type: "region", fallback: "none" });
const countries = regions.slice(0, 676).filter((r) => regionNames.of(r) !== undefined);
for (const r of countries) for (const rest of lettersOf(4)) askUnicode("tz", r.toLowerCase() + rest);
for (const key of tzKeys) {
	for (const v of lettersOf(5)) askUnicode(key, v);
}
const transformedAlias = {};
const transformedKeys = [...letters].flatMap((a) => [..."0123456789"].map((b) => a + b));
const askTransformed = (key, value) => {
	const c = canon("en-t-" + key + "-" + value);
	if (c === undefined) return;
	const prefix = "en-t-" + key;
	if (c === prefix + "-" + value) return;
	if (!c.startsWith(prefix + "-")) return;
	transformedAlias[key + "-" + value] = c.slice(prefix.length + 1);
};
for (const key of transformedKeys) for (const word of ["names", "prprname", "ungegn", "bgn"]) askTransformed(key, word);
for (const key of transformedKeys) for (const t of lettersOf(3)) askTransformed(key, t);
for (const key of ["m0"]) for (const n of [4, 5]) for (const t of lettersOf(n)) askTransformed(key, t);

// Language aliases that Intl applies only to a language with more in its tag: `tw` alone stays `tw`, `tw-AA` is `ak-AA`. And the language and
// region pairs that are one language of their own, the region gone (`sgn-BR` is `bzs`).
const qualifiedLangAlias = {};
const languageRegionRules = {};
for (const l of languages) {
	const c = canon(l + "-AA");
	const base = c?.split("-")[0];
	const bare = (langAlias[l] ?? l).split("-")[0];
	if (c !== undefined && base !== bare) qualifiedLangAlias[l] = base;
}
for (const l of languages) {
	for (const r of regions) {
		const c = canon(l + "-" + r);
		if (c !== undefined && !c.includes("-") && c !== (langAlias[l] ?? l)) languageRegionRules[l + "-" + r] = c;
	}
}
// The same pairs with a script between (`sgn-Latn-NO` is `nsi-Latn`, where `sgn-NO` is `nsl`): the script stays.
const scriptedLanguageRegionRules = {};
for (const l of new Set(Object.keys(languageRegionRules).map((pair) => pair.split("-")[0]))) {
	for (const r of regions) {
		const c = canon(l + "-Latn-" + r);
		const parts = c?.split("-");
		if (parts?.length === 2 && parts[1] === "Latn" && parts[0] !== l) scriptedLanguageRegionRules[l + "-" + r] = parts[0];
	}
}
// The one script alias: `Qaai` is `Zinh` (every other four-letter script is left as it is: asked of each).
const scriptAlias = {};
for (const a of letters) for (const b of letters) for (const c of letters) for (const d of letters) {
	const script = a.toUpperCase() + b + c + d;
	const got = canon("und-" + script);
	if (got !== undefined && got !== "und-" + script) scriptAlias[script] = got.slice(4);
}

// Variants and legacy sequences with a different canonical form.
const variantCandidates = ["aaland", "arevela", "arevmda", "bokmal", "heploc", "hepburn", "lojban", "polytoni", "posix", "gaulish", "guoyu", "hakka", "xiang", "nynorsk", "saaho", "alalc97", "polyton", "aluku", "rozaj", "1996", "1606nict", "baku1926", "boont", "fonipa", "valencia"];
const variantLangs = ["en", "ja", "hy", "zh", "art", "cel", "nb", "nn", "ca", "und"];
const variantAlias = {};
for (const v of variantCandidates) {
	for (const l of variantLangs) {
		const c = canon(l + "-" + v);
		if (c !== undefined && c !== l + "-" + v) variantAlias[l + "-" + v] = c;
	}
}

// Direction: the likely script of a language (alone, with a region) decides it.
const RTL = new Set(["Adlm", "Arab", "Hebr", "Mand", "Nkoo", "Rohg", "Samr", "Syrc", "Thaa", "Yezi"]);
const rtl = (tag) => RTL.has(new Intl.Locale(tag).maximize().script ?? "");
const rtlLangs = [];
const rtlLangRegion = [];
const ltrLangRegion = [];
for (const l of languages) {
	const alone = rtl(l);
	if (alone) rtlLangs.push(l);
	for (const r of regions.slice(0, 676 + 0)) {
		let here;
		try {
			here = rtl(l + "-" + r);
		} catch {
			continue;
		}
		if (here && !alone) rtlLangRegion.push(l + "-" + r);
		if (!here && alone) ltrLangRegion.push(l + "-" + r);
	}
}

const q = JSON.stringify;
const map = (name, doc, o) =>
	`// ${doc}\nvar ${name} = map[string]string{\n${Object.keys(o).sort().map((k) => `\t${q(k)}: ${q(o[k])},\n`).join("")}}\n\n`;
const set = (name, doc, a) =>
	`// ${doc}\nvar ${name} = map[string]struct{}{\n${[...a].sort().map((k) => `\t${q(k)}: {},\n`).join("")}}\n\n`;
const byLang = Object.entries(regionByLang).flatMap(([r, o]) => Object.entries(o).map(([l, v]) => [l + "-" + r, v]));

let out = `// Code generated by testdata/locale_tables.mjs from Node ${process.version} (ICU ${process.versions.icu}, CLDR ${process.versions.cldr}); DO NOT EDIT.\n\npackage surfaces\n\n`;
out += map("languageAliases", "languageAliases is the canonical form of each bare language tag Intl.getCanonicalLocales changes (it may add a script: sh is sr-Latn).", langAlias);
out += map("regionAliases", "regionAliases is the canonical form of each deprecated region (the default for a language with no entry in languageRegionAliases).", regionAlias);
out += map("languageRegionAliases", "languageRegionAliases is the replacement of a deprecated region for the languages whose likely region picks another (`uz-SU` is `uz-UZ`).", Object.fromEntries(byLang));
out += map("variantAliases", "variantAliases is the canonical form of a language with one variant when it differs (`art-lojban` is `jbo`, `en-posix` is `en-u-va-posix`).", variantAlias);
out += set("rtlLanguages", "rtlLanguages are the languages whose likely script (Intl.Locale maximize) is written right to left.", rtlLangs);
out += set("rtlLanguageRegions", "rtlLanguageRegions are the language-region pairs that are right to left though the bare language is not (`az-IR`).", rtlLangRegion);
out += set("ltrLanguageRegions", "ltrLanguageRegions are the language-region pairs that are left to right though the bare language is right to left.", ltrLangRegion);
out += map("qualifiedLanguageAliases", "qualifiedLanguageAliases is the canonical language of each language that Intl.getCanonicalLocales changes only when the tag has a region (or more) after it: `tw` alone stays `tw`, `tw-AA` is `ak-AA`.", qualifiedLangAlias);
out += map("languageRegionRules", "languageRegionRules is the canonical tag of each language and region pair that is a language of its own, the region gone (`sgn-BR` is `bzs`).", languageRegionRules);
out += map("nonLowercaseLanguageAliases", "nonLowercaseLanguageAliases is the canonical form of each language that Intl.getCanonicalLocales aliases only when it is not spelled all in lower case, or as the source language of a `-t-` extension: `TW` is `ak`, `tw` alone stays `tw`.", nonLowercaseLangAlias);
out += map("scriptedLanguageRegionRules", "scriptedLanguageRegionRules is the canonical language of each language and region pair with a script between that is a language of its own, the region gone and the script kept (`sgn-Latn-NO` is `nsi-Latn`).", scriptedLanguageRegionRules);
out += map("scriptAliases", "scriptAliases is the canonical form of each script Intl.getCanonicalLocales changes (`Qaai` is `Zinh`).", scriptAlias);
out += map("unicodeValueAliases", "unicodeValueAliases is the canonical value of each `-u-` keyword value that Intl.getCanonicalLocales changes, by the keyword's key and value joined by `-` (`ca-islamicc` is `islamic-civil`, `ks-primary` is `level1`); a value of several subtags is joined too (`ca-ethiopic-amete-alem` is `ethioaa`).", unicodeAlias);
out += map("transformedValueAliases", "transformedValueAliases is the canonical value of each `-t-` field value that Intl.getCanonicalLocales changes, by the field's key and value joined by `-` (`m0-names` is `prprname`).", transformedAlias);
process.stdout.write(out);
