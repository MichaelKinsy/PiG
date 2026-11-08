package runtime

// The `when` grammar of TUI commands (p2-tui-port.md section 6.3): or := and ("||" and)*; and := unary ("&&" unary)*;
// unary := "!" unary | "(" or ")" | term.
//
// pi: apps/p2/src/runtime/tui-commands.ts (parseWhen, evaluateWhen, canOverlap)

import (
	"fmt"
	"regexp"
	"strings"
)

// WhenKind is the kind of a WhenExpr node.
type WhenKind int

const (
	WhenTrue WhenKind = iota
	WhenTerm
	WhenNot
	WhenAnd
	WhenOr
)

// WhenExpr is a parsed `when` expression.
type WhenExpr struct {
	Kind  WhenKind
	Name  string      // WhenTerm
	Expr  *WhenExpr   // WhenNot
	Items []*WhenExpr // WhenAnd, WhenOr
}

// ParsedWhen is an expression with its terms (distinct, first-use order), the terms that appear un-negated, and its specificity (the
// term count, repeats included: more terms = more specific).
type ParsedWhen struct {
	Expr        *WhenExpr
	Terms       []string
	Positive    map[string]bool
	Specificity int
}

var whenTokens = regexp.MustCompile(`\|\||&&|!|\(|\)|[A-Za-z][\w:.-]*|\S`)

// ParseWhen parses source; an empty (or blank) one is always true.
func ParseWhen(source string) (ParsedWhen, error) {
	positive := map[string]bool{}
	if strings.TrimSpace(source) == "" {
		return ParsedWhen{Expr: &WhenExpr{Kind: WhenTrue}, Positive: positive}, nil
	}
	tokens := whenTokens.FindAllString(source, -1)
	var terms []string
	index := 0
	peek := func() string {
		if index < len(tokens) {
			return tokens[index]
		}
		return ""
	}
	var parseOr func(negated bool) (*WhenExpr, error)
	var parseUnary func(negated bool) (*WhenExpr, error)
	parseList := func(negated bool, separator string, kind WhenKind, item func(bool) (*WhenExpr, error)) (*WhenExpr, error) {
		first, err := item(negated)
		if err != nil {
			return nil, err
		}
		items := []*WhenExpr{first}
		for peek() == separator {
			index++
			next, err := item(negated)
			if err != nil {
				return nil, err
			}
			items = append(items, next)
		}
		if len(items) == 1 {
			return items[0], nil
		}
		return &WhenExpr{Kind: kind, Items: items}, nil
	}
	parseAnd := func(negated bool) (*WhenExpr, error) {
		return parseList(negated, "&&", WhenAnd, parseUnary)
	}
	parseOr = func(negated bool) (*WhenExpr, error) {
		return parseList(negated, "||", WhenOr, parseAnd)
	}
	parseUnary = func(negated bool) (*WhenExpr, error) {
		token := ""
		if index < len(tokens) {
			token = tokens[index]
		}
		index++
		switch {
		case token == "!":
			inner, err := parseUnary(!negated)
			if err != nil {
				return nil, err
			}
			return &WhenExpr{Kind: WhenNot, Expr: inner}, nil
		case token == "(":
			inner, err := parseOr(negated)
			if err != nil {
				return nil, err
			}
			closing := ""
			if index < len(tokens) {
				closing = tokens[index]
			}
			index++
			if closing != ")" {
				return nil, fmt.Errorf("Invalid when %q: missing )", source)
			}
			return inner, nil
		}
		if token == "" || !((token[0] >= 'A' && token[0] <= 'Z') || (token[0] >= 'a' && token[0] <= 'z')) {
			return nil, fmt.Errorf("Invalid when %q", source)
		}
		terms = append(terms, token)
		if !negated {
			positive[token] = true
		}
		return &WhenExpr{Kind: WhenTerm, Name: token}, nil
	}
	expr, err := parseOr(false)
	if err != nil {
		return ParsedWhen{}, err
	}
	if index != len(tokens) {
		return ParsedWhen{}, fmt.Errorf("Invalid when %q", source)
	}
	seen := map[string]bool{}
	var distinct []string
	for _, term := range terms {
		if !seen[term] {
			seen[term] = true
			distinct = append(distinct, term)
		}
	}
	return ParsedWhen{Expr: expr, Terms: distinct, Positive: positive, Specificity: len(terms)}, nil
}

// EvaluateWhen is whether expr holds with exactly the terms in active true.
func EvaluateWhen(expr *WhenExpr, active map[string]bool) bool {
	switch expr.Kind {
	case WhenTerm:
		return active[expr.Name]
	case WhenNot:
		return !EvaluateWhen(expr.Expr, active)
	case WhenAnd:
		for _, item := range expr.Items {
			if !EvaluateWhen(item, active) {
				return false
			}
		}
		return true
	case WhenOr:
		for _, item := range expr.Items {
			if EvaluateWhen(item, active) {
				return true
			}
		}
		return false
	}
	return true
}

// CanOverlap is whether both expressions can hold at once, given groups of mutually exclusive terms (screens, roles). More than 12 distinct
// terms are assumed to overlap.
func CanOverlap(a, b ParsedWhen, exclusive [][]string) bool {
	seen := map[string]bool{}
	var terms []string
	for _, list := range [][]string{a.Terms, b.Terms} {
		for _, term := range list {
			if !seen[term] {
				seen[term] = true
				terms = append(terms, term)
			}
		}
	}
	if len(terms) > 12 {
		return true
	}
masks:
	for mask := 0; mask < 1<<len(terms); mask++ {
		active := map[string]bool{}
		for bit, term := range terms {
			if mask&(1<<bit) != 0 {
				active[term] = true
			}
		}
		for _, group := range exclusive {
			count := 0
			for _, term := range group {
				if active[term] {
					count++
				}
			}
			if count > 1 {
				continue masks
			}
		}
		if EvaluateWhen(a.Expr, active) && EvaluateWhen(b.Expr, active) {
			return true
		}
	}
	return false
}
