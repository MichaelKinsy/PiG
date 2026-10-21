# SPDX-License-Identifier: MIT
"""Minimal terminal output parser: classifies terminal queries and keeps a character grid.

It exists so the first-frame probe can (a) tell queries from other output, (b) answer them the way a real terminal does,
and (c) know what is on the screen. It implements only what pig and phi emit: printable text, C0 controls, CSI cursor /
erase / scroll / insert-delete sequences, scroll regions, alternate-screen entry, DEC private modes 7 and 2026, and it
skips OSC / DCS / APC / PM / SOS strings. SGR and every mode it does not track are ignored.
"""
import unicodedata

QUERY, OTHER = "query", "other"


def _width(ch):
    if unicodedata.combining(ch):
        return 0
    return 2 if unicodedata.east_asian_width(ch) in ("W", "F") else 1


class Screen:
    def __init__(self, rows, cols):
        self.rows, self.cols = rows, cols
        self.grid = [[" "] * cols for _ in range(rows)]
        self.r = self.c = 0
        self.top, self.bot = 0, rows - 1
        self.wrap = True
        self.pending_wrap = False
        self.saved = (0, 0)
        self.sync_depth = 0
        self.esu_count = 0  # completed synchronized-output frames (CSI ? 2026 l)
        self.state = "ground"
        self.buf = b""
        self.utf = b""
        # parser output for the caller
        self.queries = []  # (kind, raw bytes) found since the last take_queries()
        self.nonquery_bytes = 0
        self._seq = bytearray()

    # ---- grid ops
    def _scroll_up(self, n=1):
        for _ in range(n):
            del self.grid[self.top]
            self.grid.insert(self.bot, [" "] * self.cols)

    def _scroll_down(self, n=1):
        for _ in range(n):
            del self.grid[self.bot]
            self.grid.insert(self.top, [" "] * self.cols)

    def _lf(self):
        if self.r == self.bot:
            self._scroll_up()
        elif self.r < self.rows - 1:
            self.r += 1

    def _put(self, ch):
        w = _width(ch)
        if w == 0:
            return
        if self.pending_wrap or self.c + w > self.cols:
            if self.wrap:
                self.c = 0
                self._lf()
            else:
                self.c = self.cols - w
            self.pending_wrap = False
        self.grid[self.r][self.c] = ch
        if w == 2 and self.c + 1 < self.cols:
            self.grid[self.r][self.c + 1] = ""
        self.c += w
        if self.c >= self.cols:
            self.c = self.cols - 1
            self.pending_wrap = True

    def _erase_line(self, mode):
        row = self.grid[self.r]
        lo, hi = {0: (self.c, self.cols), 1: (0, self.c + 1), 2: (0, self.cols)}.get(mode, (self.c, self.cols))
        for i in range(lo, min(hi, self.cols)):
            row[i] = " "

    def _erase_display(self, mode):
        if mode == 0:
            self._erase_line(0)
            for r in range(self.r + 1, self.rows):
                self.grid[r] = [" "] * self.cols
        elif mode == 1:
            self._erase_line(1)
            for r in range(0, self.r):
                self.grid[r] = [" "] * self.cols
        else:
            for r in range(self.rows):
                self.grid[r] = [" "] * self.cols

    # ---- CSI
    def _csi(self, raw):
        params, final = raw[2:-1], raw[-1:]
        private = params[:1] in (b"?", b">", b"<", b"=")
        body = params[1:] if private else params
        inter = b""
        while body and body[-1:] in (b"$", b" ", b"!", b'"', b"'"):
            inter = body[-1:] + inter
            body = body[:-1]
        nums = []
        for part in body.split(b";"):
            try:
                nums.append(int(part))
            except ValueError:
                nums.append(None)
        n = lambda i, d=1: (nums[i] if i < len(nums) and nums[i] not in (None, 0) else d)  # noqa: E731
        f = final.decode("latin1")
        pfx = params[:1] if private else b""
        # queries
        if f == "c" and pfx in (b"", b"0") and not inter:
            return QUERY
        if f == "c" and pfx == b">":
            return QUERY
        if f == "q" and pfx == b">":
            return QUERY
        if f == "u" and pfx == b"?":
            return QUERY
        if f == "p" and inter == b"$":
            return QUERY
        if f == "S" and pfx == b"?":
            return QUERY
        if f == "n" and pfx == b"" and nums[:1] in ([5], [6]):
            return QUERY
        if f == "t" and pfx == b"" and nums[:1] in ([14], [16], [18], [19]):
            return QUERY
        # screen effects
        self.pending_wrap = False if f in "ABCDEFGHfdJKLMPX@ST" else self.pending_wrap
        if f == "A":
            self.r = max(self.top if self.r >= self.top else 0, self.r - n(0))
        elif f == "B":
            self.r = min(self.bot if self.r <= self.bot else self.rows - 1, self.r + n(0))
        elif f == "C":
            self.c = min(self.cols - 1, self.c + n(0))
        elif f == "D":
            self.c = max(0, self.c - n(0))
        elif f == "E":
            self.r = min(self.rows - 1, self.r + n(0)); self.c = 0
        elif f == "F":
            self.r = max(0, self.r - n(0)); self.c = 0
        elif f == "G":
            self.c = min(self.cols - 1, n(0) - 1)
        elif f in "Hf":
            self.r = min(self.rows - 1, n(0) - 1); self.c = min(self.cols - 1, n(1) - 1)
        elif f == "d":
            self.r = min(self.rows - 1, n(0) - 1)
        elif f == "J":
            self._erase_display(nums[0] or 0 if nums and nums[0] is not None else 0)
        elif f == "K":
            self._erase_line(nums[0] or 0 if nums and nums[0] is not None else 0)
        elif f == "L":
            for _ in range(n(0)):
                if self.top <= self.r <= self.bot:
                    del self.grid[self.bot]; self.grid.insert(self.r, [" "] * self.cols)
        elif f == "M":
            for _ in range(n(0)):
                if self.top <= self.r <= self.bot:
                    del self.grid[self.r]; self.grid.insert(self.bot, [" "] * self.cols)
        elif f == "P":
            row = self.grid[self.r]; k = min(n(0), self.cols - self.c)
            del row[self.c:self.c + k]; row.extend([" "] * k)
        elif f == "@":
            row = self.grid[self.r]; k = min(n(0), self.cols - self.c)
            row[self.c:self.c] = [" "] * k; del row[self.cols:]
        elif f == "X":
            for i in range(self.c, min(self.cols, self.c + n(0))):
                self.grid[self.r][i] = " "
        elif f == "S":
            self._scroll_up(n(0))
        elif f == "T":
            self._scroll_down(n(0))
        elif f == "r" and not private:
            top = n(0) - 1; bot = (nums[1] if len(nums) > 1 and nums[1] else self.rows) - 1
            if 0 <= top < bot < self.rows:
                self.top, self.bot = top, bot
            self.r = self.c = 0
        elif f == "s" and not private:
            self.saved = (self.r, self.c)
        elif f == "u" and not private:
            self.r, self.c = self.saved
        elif f in "hl" and pfx == b"?":
            on = f == "h"
            for v in nums:
                if v == 7:
                    self.wrap = on
                elif v == 2026:
                    if on:
                        self.sync_depth += 1
                    else:
                        self.sync_depth = 0
                        self.esu_count += 1
                elif v in (1049, 47, 1047) and on:
                    self.grid = [[" "] * self.cols for _ in range(self.rows)]
                    self.r = self.c = 0
        return OTHER

    def feed(self, data):
        """Parse bytes; returns the list of (kind, raw) tokens that are queries (answerable) in this chunk."""
        found = []
        data = self.utf + data
        self.utf = b""
        i, n = 0, len(data)
        while i < n:
            b = data[i]
            st = self.state
            if st == "ground":
                if b == 0x1B:
                    self.state = "esc"; self._seq = bytearray([b]); i += 1
                elif b < 0x20:
                    self.nonquery_bytes += 1
                    if b == 0x0D:
                        self.c = 0; self.pending_wrap = False
                    elif b in (0x0A, 0x0B, 0x0C):
                        self._lf(); self.pending_wrap = False
                    elif b == 0x08:
                        self.c = max(0, self.c - 1); self.pending_wrap = False
                    elif b == 0x09:
                        self.c = min(self.cols - 1, (self.c // 8 + 1) * 8)
                    i += 1
                elif b < 0x80:
                    self.nonquery_bytes += 1
                    self._put(chr(b)); i += 1
                else:
                    need = 2 if b >> 5 == 6 else 3 if b >> 4 == 14 else 4 if b >> 3 == 30 else 1
                    if i + need > n:
                        self.utf = data[i:]; i = n
                    else:
                        self.nonquery_bytes += need
                        self._put(data[i:i + need].decode("utf-8", "replace")[0]); i += need
            elif st == "esc":
                self._seq.append(b); i += 1
                if b == 0x5B:
                    self.state = "csi"
                elif b == 0x5D:
                    self.state = "osc"
                elif b in (0x50, 0x5F, 0x5E, 0x58):
                    self.state = "str"
                elif 0x20 <= b <= 0x2F:
                    pass  # intermediate (charset designation); the next byte ends it
                else:
                    self.state = "ground"
                    raw = bytes(self._seq)
                    if raw == b"\x1b7":
                        self.saved = (self.r, self.c)
                    elif raw == b"\x1b8":
                        self.r, self.c = self.saved
                    elif raw == b"\x1bM":
                        if self.r == self.top:
                            self._scroll_down()
                        else:
                            self.r = max(0, self.r - 1)
                    elif raw == b"\x1bD":
                        self._lf()
                    self.nonquery_bytes += len(raw)
            elif st == "csi":
                self._seq.append(b); i += 1
                if 0x40 <= b <= 0x7E:
                    self.state = "ground"
                    raw = bytes(self._seq)
                    kind = self._csi(raw)
                    if kind == QUERY:
                        found.append((kind, raw))
                    else:
                        self.nonquery_bytes += len(raw)
            elif st in ("osc", "str"):
                self._seq.append(b); i += 1
                end = (b == 0x07 and st == "osc") or (len(self._seq) >= 2 and self._seq[-2:] == b"\x1b\\")
                if end:
                    self.state = "ground"
                    raw = bytes(self._seq)
                    if _is_string_query(raw):
                        found.append((QUERY, raw))
                    else:
                        self.nonquery_bytes += len(raw)
        return found

    def text(self):
        return "\n".join("".join(row).rstrip() for row in self.grid)

    def glyphs(self):
        return sum(1 for row in self.grid for ch in row if ch not in (" ", ""))


def _is_string_query(raw):
    body = raw[2:]
    if raw[:2] == b"\x1b]":
        body = body.rstrip(b"\x07").removesuffix(b"\x1b\\")
        parts = body.split(b";")
        return parts[-1] == b"?" and len(parts) >= 2
    if raw[:2] == b"\x1b_":  # APC: kitty graphics query a=q
        return b"a=q" in raw
    return False
