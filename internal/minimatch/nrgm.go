package minimatch

// Ports @isaacs/nonrecursive-glob-match 1.0.3 index.js: the Matcher that tests one path portion against a glob without backtracking, by simulating the pattern's state machine. The scan shortcuts of the original (a literal or prefix and suffix check, a linear walk, and a cache of the states reached) only skip work: they answer as the simulation does, so this port runs the simulation.

import (
	"slices"
	"unicode/utf8"
)

type nodeKind uint8

const (
	nodeMatch nodeKind = iota
	nodeLit
	nodeAny
	nodeStar
	nodeClass
	nodeSplit
	nodeNeg
)

// node is one state of the pattern's graph. Ids are dense, from 0 for the match node.
type node struct {
	kind  nodeKind
	id    int
	ch    rune
	fold  bool
	cls   *class
	next  *node
	out   []*node
	inner *node
}

type itemKind uint8

const (
	itemLit itemKind = iota
	itemAny
	itemStar
	itemClass
	itemExt
)

// item is a parsed piece of a glob: a code point, a wildcard, a class or an extglob with its alternatives.
type item struct {
	kind itemKind
	ch   rune
	cls  *class
	typ  rune
	alts [][]item
}

type tokKind uint8

const (
	tokLit tokKind = iota
	tokClass
	tokOpen
	tokBar
	tokClose
	tokStar
	tokAny
)

type token struct {
	kind tokKind
	ch   rune
	typ  rune
	cls  *class
}

const extTypes = "!?+*@"

func isExtType(c rune) bool {
	return c < utf8.RuneSelf && c != 0 && indexRune(extTypes, c) >= 0
}

func indexRune(s string, r rune) int {
	for i, c := range s {
		if c == r {
			return i
		}
	}
	return -1
}

func tokenize(p []rune, nocase, noext bool) []token {
	var toks []token
	for i := 0; i < len(p); {
		c := p[i]
		if c == '\\' {
			// a trailing \ is a literal \
			ch := c
			if i+1 < len(p) {
				ch = p[i+1]
			}
			toks = append(toks, token{kind: tokLit, ch: ch})
			i += 2
			continue
		}
		if c == '[' {
			if cls, consumed := parseClass(p, i, nocase); consumed > 0 {
				toks = append(toks, token{kind: tokClass, cls: cls})
				i += consumed
				continue
			}
		}
		if !noext && isExtType(c) && i+1 < len(p) && p[i+1] == '(' {
			toks = append(toks, token{kind: tokOpen, typ: c})
			i += 2
			continue
		}
		switch {
		case !noext && c == '|':
			toks = append(toks, token{kind: tokBar})
		case !noext && c == ')':
			toks = append(toks, token{kind: tokClose})
		case c == '*':
			toks = append(toks, token{kind: tokStar})
		case c == '?':
			toks = append(toks, token{kind: tokAny})
		default:
			toks = append(toks, token{kind: tokLit, ch: c})
		}
		i++
	}
	return toks
}

// pair maps each open token index to the index of its close token.
func pair(toks []token) map[int]int {
	pairs := map[int]int{}
	var stack []int
	for i, tok := range toks {
		switch tok.kind {
		case tokOpen:
			stack = append(stack, i)
		case tokClose:
			if len(stack) > 0 {
				pairs[stack[len(stack)-1]] = i
				stack = stack[:len(stack)-1]
			}
		}
	}
	return pairs
}

// build makes the alternatives between start and end. An open token that is not closed is never inside a closed extglob, so bar and close tokens are only literal at the top level.
func build(toks []token, pairs map[int]int, start, end int, top bool) [][]item {
	alts := [][]item{nil}
	cur := 0
	for i := start; i < end; i++ {
		tok := toks[i]
		close, closed := pairs[i]
		switch {
		case tok.kind == tokOpen && closed:
			sub := build(toks, pairs, i+1, close, false)
			alts[cur] = append(alts[cur], item{kind: itemExt, typ: tok.typ, alts: sub})
			i = close
		case tok.kind == tokBar && !top:
			alts = append(alts, nil)
			cur++
		case tok.kind == tokOpen:
			alts[cur] = append(alts[cur], item{kind: itemLit, ch: tok.typ}, item{kind: itemLit, ch: '('})
		case tok.kind == tokBar:
			alts[cur] = append(alts[cur], item{kind: itemLit, ch: '|'})
		case tok.kind == tokClose:
			alts[cur] = append(alts[cur], item{kind: itemLit, ch: ')'})
		case tok.kind == tokLit:
			alts[cur] = append(alts[cur], item{kind: itemLit, ch: tok.ch})
		case tok.kind == tokClass:
			alts[cur] = append(alts[cur], item{kind: itemClass, cls: tok.cls})
		case tok.kind == tokStar:
			alts[cur] = append(alts[cur], item{kind: itemStar})
		default:
			alts[cur] = append(alts[cur], item{kind: itemAny})
		}
	}
	return alts
}

// compiler numbers the nodes of one graph.
type compiler struct {
	id     int
	nocase bool
	match  *node
}

func (c *compiler) newNode(n node) *node {
	n.id = c.id
	c.id++
	return &n
}

func (c *compiler) seq(items []item, next *node) *node {
	for _, item := range slices.Backward(items) {
		next = c.item(item, next)
	}
	return next
}

func (c *compiler) alts(alts [][]item, next *node) []*node {
	out := make([]*node, len(alts))
	for i, a := range alts {
		out[i] = c.seq(a, next)
	}
	return out
}

func (c *compiler) item(it item, next *node) *node {
	switch it.kind {
	case itemLit:
		return c.newNode(node{kind: nodeLit, ch: it.ch, fold: c.nocase, next: next})
	case itemAny:
		return c.newNode(node{kind: nodeAny, next: next})
	case itemStar:
		return c.newNode(node{kind: nodeStar, next: next})
	case itemClass:
		return c.newNode(node{kind: nodeClass, cls: it.cls, next: next})
	}
	switch it.typ {
	case '@':
		return c.newNode(node{kind: nodeSplit, out: c.alts(it.alts, next)})
	case '?':
		return c.newNode(node{kind: nodeSplit, out: append([]*node{next}, c.alts(it.alts, next)...)})
	case '!':
		inner := c.newNode(node{kind: nodeSplit, out: c.alts(it.alts, c.match)})
		return c.newNode(node{kind: nodeNeg, inner: inner, next: next})
	}
	// * and +, loop back after each match
	after := c.newNode(node{kind: nodeSplit})
	entry := c.newNode(node{kind: nodeSplit, out: c.alts(it.alts, after)})
	after.out = append(after.out, next, entry)
	if it.typ == '*' {
		return after
	}
	return entry
}

func litMatch(n *node, c rune) bool {
	if c == n.ch {
		return true
	}
	return n.fold && foldEqual(n.ch, c)
}

// run is the state of a negated sub-pattern at one position.
type run struct {
	neg   *node
	state *state
}

// state is the set of threads at one position. Each node thread is only kept once, and runs with the same state are only kept once. The match node is not in threads, because it cannot step: it sets match instead.
type state struct {
	gen     uint64
	threads []*node
	match   bool
	runs    []*run
	ids     []int
}

// dead is true when nothing more can match.
func (s *state) dead() bool { return len(s.threads) == 0 && !s.match && len(s.runs) == 0 }

func sameState(a, b *state) bool {
	if len(a.ids) != len(b.ids) || len(a.runs) != len(b.runs) {
		return false
	}
	if !slices.Equal(a.ids, b.ids) {
		return false
	}
	for _, r := range a.runs {
		if !slices.ContainsFunc(b.runs, func(o *run) bool { return o.neg == r.neg && sameState(o.state, r.state) }) {
			return false
		}
	}
	return true
}

type marks struct {
	has, seen []uint64
	gen       uint64
}

// sim steps the graph over the code points of one string.
type sim struct {
	// dots: `.` and `..` only match literal dots, even with dot:true.
	dots bool
	// dotStart: the string starts with `.`, and dot:true is not set.
	dotStart bool
	marks    *marks
}

func (s *sim) newState() *state {
	s.marks.gen++
	return &state{gen: s.marks.gen}
}

func (s *sim) done(st *state) *state {
	st.ids = st.ids[:0]
	for _, n := range st.threads {
		st.ids = append(st.ids, n.id)
	}
	slices.Sort(st.ids)
	if st.match {
		st.ids = append([]int{0}, st.ids...)
	}
	return st
}

// visit is true the first time that the state walks through the node, so that loops in the graph stop.
func (s *sim) visit(st *state, n *node) bool {
	if s.marks.seen[n.id] == st.gen {
		return false
	}
	s.marks.seen[n.id] = st.gen
	return true
}

func (s *sim) addThread(st *state, n *node) bool {
	if s.marks.has[n.id] == st.gen {
		return false
	}
	s.marks.has[n.id] = st.gen
	st.threads = append(st.threads, n)
	return true
}

// restricted: wildcards only match a leading dot if dot:true. In a negated sub-pattern, this does not apply, because the negation itself cannot match a leading dot.
func (s *sim) restricted(i int, top bool) bool {
	return top && (s.dots || (s.dotStart && i == 0))
}

func (s *sim) add(st *state, n *node, i int, top bool) {
	switch n.kind {
	case nodeSplit:
		if !s.visit(st, n) {
			return
		}
		for _, o := range n.out {
			s.add(st, o, i, top)
		}
	case nodeNeg:
		if !s.visit(st, n) {
			return
		}
		sub := s.newState()
		s.add(sub, n.inner, i, false)
		s.done(sub)
		s.addRun(st, &run{neg: n, state: sub}, i, top)
	case nodeStar:
		// a * can not start a dotfile, even if it matches nothing
		if s.restricted(i, top) {
			return
		}
		if s.addThread(st, n) {
			s.add(st, n.next, i, top)
		}
	case nodeMatch:
		st.match = true
	default:
		s.addThread(st, n)
	}
}

func (s *sim) addRun(st *state, r *run, i int, top bool) {
	for _, o := range st.runs {
		if o.neg == r.neg && sameState(o.state, r.state) {
			return
		}
	}
	st.runs = append(st.runs, r)
	if !r.state.match {
		s.add(st, r.neg.next, i, top)
	}
}

// step is the threads after code point c, at index i.
func (s *sim) step(st *state, c rune, i int, top bool) *state {
	next := s.newState()
	for _, th := range st.threads {
		switch th.kind {
		case nodeLit:
			if litMatch(th, c) {
				s.add(next, th.next, i+1, top)
			}
		case nodeAny:
			if !s.restricted(i, top) {
				s.add(next, th.next, i+1, top)
			}
		case nodeClass:
			if !s.restricted(i, top) && th.cls.test(c) {
				s.add(next, th.next, i+1, top)
			}
		case nodeStar:
			if !s.restricted(i, top) {
				s.add(next, th, i+1, top)
			}
		}
	}
	if !s.restricted(i, top) {
		for _, r := range st.runs {
			// a sub-pattern with no threads can not match again, so the run stays the same
			if r.state.dead() {
				s.addRun(next, r, i+1, top)
				continue
			}
			sub := s.step(r.state, c, i, false)
			s.addRun(next, &run{neg: r.neg, state: sub}, i+1, top)
		}
	}
	return s.done(next)
}

// matcher tests one path portion against a glob, as the Matcher class of nonrecursive-glob-match.
type matcher struct {
	pattern string
	dot     bool
	start   *node
	size    int
	// literal is the string to compare when the pattern has no magic.
	literal    []rune
	hasLiteral bool
	nocase     bool
}

func newMatcher(pattern string, nocase, dot bool) *matcher {
	p := []rune(pattern)
	// a pattern that is only an empty extglob is a literal string, except for `!()`, which matches any non-empty string
	literal := len(p) == 3 && indexRune("?*+@", p[0]) >= 0 && p[1] == '(' && p[2] == ')'
	toks := tokenize(p, nocase, literal)
	alts := build(toks, pair(toks), 0, len(toks), true)
	items := alts[0]
	c := &compiler{id: 1, nocase: nocase, match: &node{kind: nodeMatch}}
	m := &matcher{pattern: pattern, dot: dot, nocase: nocase, start: c.seq(items, c.match)}
	m.size = c.id
	if !slices.ContainsFunc(items, func(it item) bool { return it.kind != itemLit }) {
		m.hasLiteral = true
		m.literal = make([]rune, len(items))
		for i, it := range items {
			m.literal[i] = it.ch
		}
	}
	return m
}

// test is Matcher#testUnchecked for a string that is one path portion (it has no `/`).
func (m *matcher) test(s string) bool {
	chars := []rune(s)
	if m.hasLiteral {
		if len(chars) != len(m.literal) {
			return false
		}
		for i, c := range chars {
			if c != m.literal[i] && (!m.nocase || !foldEqual(m.literal[i], c)) {
				return false
			}
		}
		return true
	}
	mk := &marks{has: make([]uint64, m.size), seen: make([]uint64, m.size)}
	// `.` and `..` only match literal dots, even with dot:true
	dots := s == "." || s == ".."
	sm := &sim{dots: dots, dotStart: !m.dot && len(s) > 0 && s[0] == '.', marks: mk}
	st := sm.newState()
	sm.add(st, m.start, 0, true)
	sm.done(st)
	for i, c := range chars {
		if st.dead() {
			return false
		}
		st = sm.step(st, c, i, true)
	}
	return st.match
}
