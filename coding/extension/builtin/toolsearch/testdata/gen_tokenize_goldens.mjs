// Generates tokenize_goldens.json: Pi's own tokenize at commit 79006f06 (earendil-works/pi#11037), the Unicode word segmentation PiG carries
// (D217), over probes of every script class. The coding agent's (packages/coding-agent/src/extensions/tool-search/tool.ts) and durable's
// (packages/durable/src/search.ts) tokenizers are two copies in Pi; the script fails when they answer differently, so one file serves the two PiG
// packages (copy it to durable/internal/search/testdata/).
//
//   PI_CHECKOUT=<pi checkout at 79006f06> node gen_tokenize_goldens.mjs > tokenize_goldens.json
//
// Run it with Node 26 / ICU 78.3 (Unicode 17), the ICU that internal/wordsegmenter ports. Each probe has a class; the dictionary classes (han, hiragana,
// katakana, thai, lao, khmer, myanmar) are the scripts ICU segments with a dictionary, which a Piglet Binary built with pig_strip_word_dictionaries leaves out.
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

if (process.versions.icu !== "78.3") throw new Error(`Expected ICU 78.3, have ${process.versions.icu}`);
const here = dirname(fileURLToPath(import.meta.url));
const checkout = process.env.PI_CHECKOUT;
const dir = mkdtempSync(join(tmpdir(), "pi-tokenize-"));
// tool.ts imports typebox only to declare the tool's input schema; tokenize does not use it.
writeFileSync(join(dir, "tool.ts"), readFileSync(join(checkout, "packages/coding-agent/src/extensions/tool-search/tool.ts"), "utf8").replace('import { type Static, Type } from "typebox";', "const Type = new Proxy({}, { get: () => () => ({}) });"));
const coding = await import(pathToFileURL(join(dir, "tool.ts")).href);
const durable = await import(pathToFileURL(join(checkout, "packages/durable/src/search.ts")).href);

let seed = 79006;
const rand = (n) => {
	seed = (seed * 1664525 + 1013904223) % 4294967296;
	return Math.floor((seed / 4294967296) * n);
};
const pick = (items) => items[rand(items.length)];

// Phrases per class: the texts of Pi's test, and ordinary words and sentences.
const phrases = {
	latin: ["Éléments cafés", "café", "cafe\u0301", "CAFÉ", "résumé cafés", "Straße", "naïve Ærø", "İstanbul", "ǅungla", "ﬁle ﬂow", "Ångström", "Rechercher un café", "Dvořák Łódź", "tiếng Việt", "Việt Nam", "ñandú señor", "Ça va très bien", "x\u0301y", "Ωmega"],
	cyrillic: ["Поиск документов", "поиск", "Документы и файлы", "Ёлка ёж", "Київ Україна", "български език", "Ӏ ӏ"],
	greek: ["Αναζήτηση εγγράφων", "ΣΑΣ", "Ελληνικά κείμενα", "ά έ ή", "ΌΣΟΣ"],
	arabic: ["البحث عن الملفات", "اللغة العربية", "بحث", "مرحبا بالعالم", "فارسی و اردو", "٣٤٥ ١٢"],
	hebrew: ["חיפוש מסמכים", "עברית", "שלום עולם", "אבג דהו"],
	devanagari: ["फ़ाइल खोजें", "हिन्दी भाषा", "नमस्ते दुनिया", "क्षत्रिय", "संस्कृतम्"],
	bengali: ["ফাইল অনুসন্ধান", "বাংলা ভাষা"],
	gurmukhi: ["ਫਾਈਲ ਖੋਜ", "ਪੰਜਾਬੀ"],
	gujarati: ["ફાઇલ શોધ", "ગુજરાતી ભાષા"],
	tamil: ["கோப்பு தேடல்", "தமிழ் மொழி"],
	telugu: ["ఫైల్ శోధన", "తెలుగు భాష"],
	kannada: ["ಕಡತ ಹುಡುಕಾಟ", "ಕನ್ನಡ ಭಾಷೆ"],
	malayalam: ["ഫയൽ തിരയൽ", "മലയാളം ഭാഷ"],
	sinhala: ["ගොනු සෙවීම", "සිංහල"],
	hangul: ["문서 검색", "한국어 문서", "검색어 입력", "ᄒᆞᆫ글", "ㅎㅏㄴ", "ﾊﾝｸﾞﾙ", "한\u0301국"],
	armenian: ["Փնտրել փաստաթղթեր", "հայերեն"],
	georgian: ["დოკუმენტების ძიება", "ქართული"],
	ethiopic: ["ሰነዶችን ፈልግ", "አማርኛ"],
	tibetan: ["ཡིག་ཆ་འཚོལ་བ", "བོད་སྐད"],
	mongolian: ["ᠮᠣᠩᠭᠣᠯ ᠬᠡᠯᠡ"],
	cherokee: ["ᏣᎳᎩ ᎦᏬᏂᎯᏍᏗ"],
	numbers: ["123", "٣٤٥", "१२३", "3a 4b", "a1b2", "①②③", "½ ¼", "x²", "1,000.50", "ⅷ Ⅻ", "𝟏𝟐𝟑"],
	han: ["搜索仓库问题", "搜索记录", "文書", "搜索GitHub_repoIssues résumé cafés", "学生", "我爱北京天安门", "中华人民共和国", "未知", "数据库连接", "測試中文分詞", "繁體中文搜尋", "東京都"],
	hiragana: ["を", "ひらがな", "これはテストです", "ありがとうございます", "がぎぐ", "か\u3099"],
	katakana: ["ツール", "カタカナ", "コンピューター", "ｶﾀｶﾅ", "テスト検索", "ー", "ヴァイオリン"],
	"han-kana": ["文書を検索", "文書を検索します", "日本語の文章", "学生です", "検索ツール", "東京タワーに行く", "仓库问题を検索"],
	thai: ["ค้นหาเอกสาร", "ภาษาไทย", "สวัสดีครับ", "ประเทศไทย", "การค้นหาไฟล์", "เอกสาร"],
	lao: ["ຄົ້ນຫາເອກະສານ", "ພາສາລາວ", "ສະບາຍດີ"],
	khmer: ["ស្វែងរកឯកសារ", "ភាសាខ្មែរ", "សួស្តី"],
	myanmar: ["စာရွက်စာတမ်းရှာဖွေ", "မြန်မာဘာသာ", "မင်္ဂလာပါ"],
	mixed: ["搜索GitHub_repoIssues résumé cafés", "Поиск GitHub 搜索", "listIssues для 文書", "getHTTPResponse ค้นหา", "メールsend_message", "tool_搜索_ツール", "app 앱 アプリ", "café搜索", "ABC日本語DEF", "x_ค้นหา_y", "Mixed3Case数据"],
	"ascii-and-symbols": [" \t，。!? 😀 ", "", "   ", "repo.issues/search:queries", "listIssues for the GitHub_repo", "searches queries HTTPServer", "a-b_c.d", "😀😀", "—…“”", "\u200b\u200d\u200c", "\u0301\u0301", "x\u200dy", "don't can’t", "3.14 v1.2.3", "$100 €5", "emoji 👨‍👩‍👧‍👦 family", "tab\tnew\nline\r\n", "\ud800", "a\udc00b", "\ud800\u0301", "𠀀𠀁", "𝒜𝒷𝒸"],
};
const dictionary = new Set(["han", "hiragana", "katakana", "han-kana", "thai", "lao", "khmer", "myanmar"]);
const mixedDictionary = new Set(["mixed"]);

const probes = new Map();
const add = (klass, text) => {
	if (!probes.has(text)) probes.set(text, klass);
};
for (const [klass, texts] of Object.entries(phrases)) for (const text of texts) add(klass, text);

// Random texts per class: code points of the class's script (letters, marks and digits), joined by separators, identifier boundaries and other scripts.
const scripts = {
	latin: "Latin", cyrillic: "Cyrillic", greek: "Greek", arabic: "Arabic", hebrew: "Hebrew", devanagari: "Devanagari", bengali: "Bengali", gurmukhi: "Gurmukhi", gujarati: "Gujarati", tamil: "Tamil", telugu: "Telugu",
	kannada: "Kannada", malayalam: "Malayalam", sinhala: "Sinhala", hangul: "Hangul", armenian: "Armenian", georgian: "Georgian", ethiopic: "Ethiopic", tibetan: "Tibetan", mongolian: "Mongolian",
	cherokee: "Cherokee", han: "Han", hiragana: "Hiragana", katakana: "Katakana", thai: "Thai", lao: "Lao", khmer: "Khmer", myanmar: "Myanmar",
};
const pools = {};
for (const [klass, script] of Object.entries(scripts)) {
	const re = new RegExp(`^[\\p{Script_Extensions=${script}}&&[\\p{L}\\p{M}\\p{N}]]$`, "v");
	const pool = [];
	for (let cp = 0; cp <= 0x10ffff; cp++) {
		if (cp >= 0xd800 && cp <= 0xdfff) continue;
		const ch = String.fromCodePoint(cp);
		if (re.test(ch)) pool.push(ch);
	}
	if (pool.length === 0) throw new Error(`empty pool for ${script}`);
	pools[klass] = pool;
}
// For Han, Hiragana and Katakana, most random draws come from the common ranges, where dictionary words are; the rest from the whole script.
const common = { han: (ch) => ch.codePointAt(0) >= 0x4e00 && ch.codePointAt(0) <= 0x9fff, hiragana: (ch) => ch.codePointAt(0) <= 0x309f, katakana: (ch) => ch.codePointAt(0) >= 0x30a0 && ch.codePointAt(0) <= 0x30ff };
const separators = [" ", " ", " ", "", "", "_", "-", ".", "'", ",", "\u200d", "\u200c", "\u0301", "A", "a1", "Ab", "ABc", "x", "7", "😀", "\t", "\u00a0", "/", "：", "。"];
const sample = (klass) => {
	const pool = pools[klass];
	const commonPool = common[klass] ? pool.filter(common[klass]) : pool;
	const words = 1 + rand(4);
	let text = "";
	for (let w = 0; w < words; w++) {
		for (let i = 0, n = 1 + rand(9); i < n; i++) text += pick(rand(5) === 0 ? pool : commonPool);
		if (w < words - 1 || rand(3) === 0) text += pick(separators);
	}
	return text;
};
for (const klass of Object.keys(scripts)) for (let i = 0; i < 100; i++) add(klass, sample(klass));
// Two scripts in one text, as in mixed-language tool descriptions.
const classes = Object.keys(scripts);
for (let i = 0; i < 300; i++) add("mixed", `${sample(pick(classes))}${pick(separators)}${sample(pick(classes))}${rand(2) === 0 ? pick(["GitHub", "listIssues", "x_y", "URLs", "Mixed3Case"]) : ""}`);

// The Southeast Asian fixture of internal/wordsegmenter: dictionary words and combinations, truncated runs, minimum spans, combining marks, script switches.
const fixture = JSON.parse(readFileSync(join(here, "../../../../../internal/wordsegmenter/testdata/sea-icu78.json"), "utf8"));
const perScript = { thai: 0, lao: 0, khmer: 0, burmese: 0, mixed: 0, "rule-tailoring": 0, "engine-selection": 0 };
for (const c of fixture.cases) {
	if (!(c.script in perScript)) continue;
	const n = perScript[c.script]++;
	const keep = c.script === "mixed" || c.script === "engine-selection" ? true : n % 5 === 0;
	if (keep) add(c.script === "burmese" ? "myanmar" : c.script === "rule-tailoring" ? "rule-tailoring" : c.script === "engine-selection" ? "mixed" : c.script, c.text);
}

const out = [];
for (const [text, klass] of probes) {
	const tokens = coding.tokenize(text);
	const same = durable.tokenize(text);
	if (JSON.stringify(tokens) !== JSON.stringify(same)) throw new Error(`coding and durable tokenize differ for ${JSON.stringify(text)}`);
	out.push({ class: klass, text, tokens });
}
console.log(JSON.stringify(out));
