// Runs Node's own fs.realpathSync walk and path.win32.resolve against a fake Windows filesystem.
// The walk in realpathSync below is copied from Node's lib/fs.js (v26.7.0 fs.realpathSync.toString(), without the
// experimental VFS hook and the realpath cache, neither of which changes the result). splitRoot, nextPart and
// isFileType are the same file's Windows branches. Input on stdin: {cwd, env, entries, calls}.
// Output: one JSON element per call, {"ok":true,"path":...} or {"ok":false}.
import path from 'node:path';
import fs from 'node:fs';

const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const pathModule = path.win32;
process.cwd = () => input.cwd;
// POSIX process.env cannot hold the "=D:" names that path.win32.resolve reads, so swap in a plain object.
Object.defineProperty(process, 'env', { value: { ...(input.env ?? {}) } });

const S_IFMT = 0o170000, S_IFREG = 0o100000, S_IFDIR = 0o040000, S_IFLNK = 0o120000, S_IFIFO = 0o010000, S_IFSOCK = 0o140000;
const key = (p) => {
  let k = p.replaceAll('/', '\\').toLowerCase();
  if (k.length > 1 && k.endsWith('\\') && !/^[a-z]:\\$/.test(k)) k = k.replace(/\\+$/, '');
  return k;
};
const entries = new Map(Object.entries(input.entries).map(([p, e]) => [key(p), e]));
const enoent = (p) => Object.assign(new Error(`ENOENT: ${p}`), { code: 'ENOENT' });
const modeOf = (e) => (e.t === 'link' ? S_IFLNK : e.t === 'file' ? S_IFREG : S_IFDIR);

const statValues = [0, S_IFDIR];
const binding = {
  lstat(p) {
    const e = entries.get(key(p));
    if (e === undefined) throw enoent(p);
    return [0, modeOf(e)];
  },
  stat(p) {
    const e = entries.get(key(p));
    if (e === undefined || e.dangling) throw enoent(p);
    return [0, modeOf(e)];
  },
  readlink(p) {
    const e = entries.get(key(p));
    if (e === undefined || e.t !== 'link') throw enoent(p);
    return e.target;
  },
};

const isWindows = true;
const splitRootRe = /^(?:[a-zA-Z]:|[\\/]{2}[^\\/]+[\\/][^\\/]+)?[\\/]*/;
function splitRoot(str) {
  return splitRootRe.exec(str)[0];
}
function nextPart(p, i) {
  for (; i < p.length; ++i) {
    const ch = p.charCodeAt(i);
    if (ch === 92 || ch === 47) return i;
  }
  return -1;
}
function isFileType(stats, fileType) {
  let mode = stats[1];
  if (typeof mode === 'bigint') mode = Number(mode);
  return (mode & S_IFMT) === fileType;
}
const StringPrototypeSlice = (s, ...a) => s.slice(...a);

function realpathSync(p) {
  p = pathModule.resolve(p);

  const cache = undefined;
  const seenLinks = new Map();
  const knownHard = new Set();

  let pos;
  let current;
  let base;
  let previous;

  current = base = splitRoot(p);
  pos = current.length;

  if (isWindows) {
    const out = binding.lstat(base, false, undefined, true /* throwIfNoEntry */);
    if (out === undefined) {
      return;
    }
    knownHard.add(base);
  }

  while (pos < p.length) {
    const result = nextPart(p, pos);
    previous = current;
    if (result === -1) {
      const last = StringPrototypeSlice(p, pos);
      current += last;
      base = previous + last;
      pos = p.length;
    } else {
      current += StringPrototypeSlice(p, pos, result + 1);
      base = previous + StringPrototypeSlice(p, pos, result);
      pos = result + 1;
    }

    if (knownHard.has(base) || cache?.get(base) === base) {
      if (isFileType(statValues, S_IFIFO) ||
          isFileType(statValues, S_IFSOCK)) {
        break;
      }
      continue;
    }

    let resolvedLink;
    const maybeCachedResolved = cache?.get(base);
    if (maybeCachedResolved) {
      resolvedLink = maybeCachedResolved;
    } else {
      const stats = binding.lstat(base, true, undefined, true /* throwIfNoEntry */);
      if (stats === undefined) {
        return;
      }

      if (!isFileType(stats, S_IFLNK)) {
        knownHard.add(base);
        cache?.set(base, base);
        continue;
      }

      let linkTarget = null;
      let id;
      if (!isWindows) {
        id = `${stats[0]}:${stats[7]}`;
        if (seenLinks.has(id)) {
          linkTarget = seenLinks.get(id);
        }
      }
      if (linkTarget === null) {
        binding.stat(base, false, undefined, true);
        linkTarget = binding.readlink(base, undefined);
      }
      resolvedLink = pathModule.resolve(previous, linkTarget);

      cache?.set(base, resolvedLink);
      if (!isWindows) seenLinks.set(id, linkTarget);
    }

    p = pathModule.resolve(resolvedLink, StringPrototypeSlice(p, pos));

    current = base = splitRoot(p);
    pos = current.length;

    if (isWindows && !knownHard.has(base)) {
      const out = binding.lstat(base, false, undefined, true /* throwIfNoEntry */);
      if (out === undefined) {
        return;
      }
      knownHard.add(base);
    }
  }

  return p;
}

const out = [];
for (const call of input.calls) {
  if (call.resolve) {
    out.push({ ok: true, path: pathModule.resolve(...call.resolve) });
    continue;
  }
  try {
    out.push({ ok: true, path: realpathSync(call.realpath) });
  } catch {
    out.push({ ok: false });
  }
}
process.stdout.write(JSON.stringify(out));
