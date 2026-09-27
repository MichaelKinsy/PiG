// Hidden files in a directory entry are skipped.
export default function () {
  throw new Error("hidden directory entries must not load");
}
