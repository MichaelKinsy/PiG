// Keep Pi's two shared native segmenters, but construct them only when a caller segments text or explicitly requests the native instance.
const Segmenter = Intl.Segmenter;
let grapheme;
let word;
export function getGraphemeSegmenter() {
  return grapheme ??= new Segmenter(undefined, { granularity: "grapheme" });
}
export function getWordSegmenter() {
  return word ??= new Segmenter(undefined, { granularity: "word" });
}
export const graphemeSegmenter = { segment(text) { return getGraphemeSegmenter().segment(text); } };
export const wordSegmenter = { segment(text) { return getWordSegmenter().segment(text); } };
