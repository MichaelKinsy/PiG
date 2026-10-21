const TEXTS = [
  "one\ntwo\nthree\nfour\nfive",
  "a long line that wraps around the edge of a narrow terminal several times over and over",
  "\tindented\n\t\tdeeper\ttab",
  "\x1b[31mred spans\nlines\x1b[39m plain and more words to wrap here",
  "漢字漢字漢字漢字漢字漢字\nmixed 漢字 width",
  "   \n  ",
  "x",
  "line\r\nwith\rcarriage\nreturns",
  "ünï\u0301cödé and \u{1f600} emoji \u{1f468}\u200d\u{1f469}\u200d\u{1f467} here",
];

export const cases = [];
for (const [t, text] of TEXTS.entries()) {
  for (const width of [0, 1, 2, 5, 12, 40]) {
    for (const max of [0, 1, 2, 3, 100]) {
      for (const keep of [undefined, "start", "end"]) {
        for (const paddingX of [undefined, 0, 1, 3]) {
          cases.push({ name: `text ${t} width ${width} max ${max} keep ${keep} pad ${paddingX}`, args: [text, max, width, paddingX, keep] });
        }
      }
    }
  }
}
cases.push({ name: "empty text", args: ["", 3, 10] });
cases.push({ name: "negative max keeps all but the first", args: ["a\nb\nc\nd", -1, 10] });
cases.push({ name: "negative max from the start", args: ["a\nb\nc\nd", -1, 10, 0, "start"] });
cases.push({ name: "unknown keep keeps the end", args: ["a\nb\nc\nd", 2, 10, 0, "middle"] });
