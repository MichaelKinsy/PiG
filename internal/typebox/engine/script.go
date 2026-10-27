package engine

import (
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// ---- script/token ----
//
// TypeBox's parser results are `[value, rest]` on success and `[]` on failure; pr is that pair.

type pr struct {
	ok    bool
	value any
	rest  string
}

type parser func(input string) pr

func success(value any, rest string) pr { return pr{true, value, rest} }

var failed = pr{}

// trim is Trim: leading whitespace and comments, newlines included.
func trim(input string) string {
	for {
		trimmed := jsstring.TrimStart(input)
		switch {
		case strings.HasPrefix(trimmed, "/*"):
			input = discardMultilineComment(trimmed[2:])
		case strings.HasPrefix(trimmed, "//"):
			input = discardLineComment(trimmed[2:])
		default:
			return trimmed
		}
	}
}

func discardMultilineComment(input string) string {
	if _, after, ok := strings.Cut(input, "*/"); ok {
		return after
	}
	return ""
}

func discardLineComment(input string) string {
	if i := strings.Index(input, "\n"); i >= 0 {
		return input[i:]
	}
	return ""
}

// trimWhitespace is TrimWhitespace: like trim but a newline stays (it is a token).
func trimWhitespace(input string) string {
	for {
		trimmed := strings.TrimLeft(input, " \t\r\f\v")
		switch {
		case strings.HasPrefix(trimmed, "/*"):
			input = discardMultilineComment(trimmed[2:])
		case strings.HasPrefix(trimmed, "//"):
			input = discardLineComment(trimmed[2:])
		default:
			return trimmed
		}
	}
}

func take(variants []string, input string) pr {
	for _, v := range variants {
		if strings.HasPrefix(input, v) {
			return success(v, input[len(v):])
		}
	}
	return failed
}

func tokConst(c string) parser {
	return func(input string) pr {
		switch {
		case c == "":
			return success("", input)
		case strings.HasPrefix(c, "\n"):
			return take([]string{c}, trimWhitespace(input))
		case strings.HasPrefix(c, " "):
			return take([]string{c}, input)
		}
		return take([]string{c}, trim(input))
	}
}

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func tokIdent(input string) pr {
	input = trim(input)
	if input == "" || (!isAlpha(input[0]) && input[0] != '_' && input[0] != '$') {
		return failed
	}
	i := 1
	for i < len(input) && (isAlpha(input[i]) || isDigit(input[i]) || input[i] == '_' || input[i] == '$') {
		i++
	}
	return success(input[:i], input[i:])
}

// many is Many(allowed digits and underscore, discard underscore).
func manyDigits(input string) (string, string) {
	var b strings.Builder
	i := 0
	for i < len(input) && (isDigit(input[i]) || input[i] == '_') {
		if input[i] != '_' {
			b.WriteByte(input[i])
		}
		i++
	}
	return b.String(), input[i:]
}

func tokUnsignedInteger(input string) pr {
	input = trim(input)
	if strings.HasPrefix(input, "0") {
		return success("0", input[1:])
	}
	if input == "" || input[0] < '1' || input[0] > '9' {
		return failed
	}
	digits, rest := manyDigits(input[1:])
	return success(input[:1]+digits, rest)
}

func takeSign(input string) (string, string) {
	if strings.HasPrefix(input, "-") {
		return "-", input[1:]
	}
	return "", input
}

func tokInteger(input string) pr {
	sign, rest := takeSign(trim(input))
	r := tokUnsignedInteger(rest)
	if !r.ok {
		return failed
	}
	return success(sign+r.value.(string), r.rest)
}

func tokUnsignedNumber(input string) pr {
	input = trim(input)
	fractional := func(in string) (string, string, bool) {
		digits, rest := manyDigits(in)
		return digits, rest, digits != ""
	}
	if strings.HasPrefix(input, ".") {
		digits, rest, ok := fractional(input[1:])
		if !ok {
			return failed
		}
		return success("0."+digits, rest)
	}
	r := tokUnsignedInteger(input)
	if !r.ok {
		return failed
	}
	integer := r.value.(string)
	if strings.HasPrefix(r.rest, ".") {
		if digits, rest, ok := fractional(r.rest[1:]); ok {
			return success(integer+"."+digits, rest)
		}
		return success(integer, r.rest[1:])
	}
	return success(integer, r.rest)
}

func tokNumber(input string) pr {
	sign, rest := takeSign(trim(input))
	r := tokUnsignedNumber(rest)
	if !r.ok {
		return failed
	}
	return success(sign+r.value.(string), r.rest)
}

func tokBigInt(input string) pr {
	r := tokInteger(input)
	if !r.ok || !strings.HasPrefix(r.rest, "n") {
		return failed
	}
	return success(r.value.(string), r.rest[1:])
}

// until is Until(end, input): the text up to the first sentinel; failing when the input ends first.
func until(end []string, input string) pr {
	for i := range len(input) {
		for _, s := range end {
			if strings.HasPrefix(input[i:], s) {
				return success(input[:i], input[i:])
			}
		}
	}
	return failed
}

func until1(end []string, input string) pr {
	r := until(end, input)
	if !r.ok || r.value.(string) == "" {
		return failed
	}
	return r
}

func tokString(quotes []string) parser {
	return func(input string) pr {
		input = trim(input)
		initial := take(quotes, input)
		if !initial.ok {
			return failed
		}
		quote := initial.value.(string)
		// Span(quote, quote, false, ...): the text up to the closing quote on the same line.
		rest := trim(quote + initial.rest)
		start := take([]string{quote}, rest)
		if !start.ok {
			return failed
		}
		u := until([]string{"\n", quote}, start.rest)
		if !u.ok {
			return failed
		}
		end := take([]string{quote}, u.rest)
		if !end.ok {
			return failed
		}
		return success(u.value, end.rest)
	}
}

// ---- combinators (the shapes of the generated parser) ----

// seq is a sequence: the value is the list of the parts' values.
func seq(parts ...parser) parser {
	return func(input string) pr {
		values := make([]any, 0, len(parts))
		for _, p := range parts {
			r := p(input)
			if !r.ok {
				return failed
			}
			values = append(values, r.value)
			input = r.rest
		}
		return success(values, input)
	}
}

// alt is an ordered choice: the first part that parses, as it is.
func alt(parts ...parser) parser {
	return func(input string) pr {
		for _, p := range parts {
			if r := p(input); r.ok {
				return r
			}
		}
		return failed
	}
}

// wrap is a one-part sequence: `[value]`.
func wrap(p parser) parser {
	return func(input string) pr {
		r := p(input)
		if !r.ok {
			return failed
		}
		return success([]any{r.value}, r.rest)
	}
}

// empty always parses and consumes nothing: `[[], input]`.
func empty(input string) pr { return success([]any{}, input) }

// star is the `X_0` loop of a list: zero or more of p, the list of their values.
func star(p parser) parser {
	return func(input string) pr {
		result := []any{}
		for {
			r := p(input)
			if !r.ok {
				return success(result, input)
			}
			result = append(result, r.value)
			input = r.rest
		}
	}
}

// mapped applies a rule's mapping to a successful parse.
func mapped(p parser, mapping func(any) any) parser {
	return func(input string) pr {
		r := p(input)
		if !r.ok {
			return failed
		}
		return success(mapping(r.value), r.rest)
	}
}

// ref defers to a rule defined later (the grammar is mutually recursive).
func ref(p *parser) parser { return func(input string) pr { return (*p)(input) } }

// ---- script/mapping ----

func idx(v any, i int) any {
	a, ok := v.([]any)
	if !ok {
		return get(v, strconv.Itoa(i))
	}
	return elem(a, i)
}

func lengthOf(v any) int {
	switch x := v.(type) {
	case []any:
		return len(x)
	case string:
		return jsstring.Length(x)
	}
	if n, ok := get(v, "length").(float64); ok {
		return int(n)
	}
	return -1
}

func deferred1(action string, t any) *Obj { return tDeferred(action, []any{t}, NewObj()) }

func intrinsicOrCall(refName any, parameters any) any {
	p := func(i int) any { return idx(parameters, i) }
	name, _ := refName.(string)
	switch name {
	case "Array":
		return tArray(p(0), nil)
	case "Capitalize", "ConstructorParameters", "Evaluate", "InstanceType", "Lowercase", "NonNullable", "Parameters", "Partial", "Required", "ReturnType", "Uncapitalize", "Uppercase", "KeyOf":
		return deferred1(name, p(0))
	case "Readonly":
		return deferred1("ReadonlyObject", p(0))
	case "Exclude", "Extract", "Index", "Omit", "Pick", "Record":
		return tDeferred(name, []any{p(0), p(1)}, NewObj())
	}
	return tCallConstruct(tRef(refName, nil), parameters)
}

func delimited(input any) any {
	if lengthOf(input) == 3 {
		out := []any{idx(input, 0)}
		for _, left := range asArray(idx(input, 1), "length") {
			out = append(out, idx(left, 1))
		}
		return out
	}
	return []any{}
}

func parseFloatToken(text any) float64 {
	f, _ := strconv.ParseFloat(text.(string), 64)
	if math.IsNaN(f) {
		return f
	}
	return f
}

func factorIndexArray(t any, indexArray any) any {
	result := t
	for _, left := range asArray(indexArray, "reduce") {
		switch lengthOf(left) {
		case 1:
			result = tDeferred("Index", []any{result, idx(left, 0)}, NewObj())
		case 0:
			result = tArray(result, nil)
		default:
			unreachable()
		}
	}
	return result
}

func factorExtends(t any, extend any) any {
	if lengthOf(extend) == 3 {
		return tDeferred("Conditional", []any{t, idx(extend, 0), idx(extend, 1), idx(extend, 2)}, NewObj())
	}
	return t
}

func factorWith(t any, withClause any) any {
	if isArray(withClause) && lengthOf(withClause) == 0 {
		return t
	}
	return tDeferred("With", []any{t, withClause}, NewObj())
}

func exprBinary(left any, rest any) any {
	if lengthOf(rest) == 3 {
		operator, right, next := idx(rest, 0), idx(rest, 1), idx(rest, 2)
		schema := exprBinary(right, next)
		if operator == "&" {
			if isIntersect(schema) {
				return tIntersect(append([]any{left}, asArray(get(schema, "allOf"), "length")...), nil)
			}
			return tIntersect([]any{left, schema}, nil)
		}
		if operator == "|" {
			if isUnion(schema) {
				return tUnion(append([]any{left}, asArray(get(schema, "anyOf"), "length")...), nil)
			}
			return tUnion([]any{left, schema}, nil)
		}
		unreachable()
	}
	return left
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case nil:
		return false
	case string:
		return x != ""
	case float64:
		return x != 0 && x == x
	case *undefined:
		return false
	}
	return true
}

func applyModifier(isReadonly, isOptional bool, t any) any {
	switch {
	case isReadonly && isOptional:
		return deferred1("AddReadonly", deferred1("AddOptional", t))
	case isReadonly:
		return deferred1("AddReadonly", t)
	case isOptional:
		return deferred1("AddOptional", t)
	}
	return t
}

func propertiesReduce(propertyList any) []any {
	var props, patterns any = NewObj(), NewObj()
	for _, left := range asArray(propertyList, "reduce") {
		if hasKey(left, integerKey) || hasKey(left, numberKey) || hasKey(left, stringKey) {
			patterns = Assign(patterns, left)
		} else {
			props = Assign(props, left)
		}
	}
	return []any{props, patterns}
}

func objectOptionsFor(patternProperties any) any {
	if len(ownKeys(patternProperties)) == 0 {
		return NewObj()
	}
	return NewObj("patternProperties", patternProperties)
}

func mappedModifier(input any, name string) any {
	switch {
	case lengthOf(input) == 2 && idx(input, 0) == "-":
		return "remove"
	case lengthOf(input) == 2 && idx(input, 0) == "+":
		return "add"
	case lengthOf(input) == 1:
		return "add"
	}
	return "none"
}

func applyMappedModifier(state any, add, remove string, t any) any {
	switch state {
	case "remove":
		return deferred1(remove, t)
	case "add":
		return deferred1(add, t)
	}
	return t
}

func identity(v any) any { return v }

// ---- script/parser ----

var (
	rGenericParameterExtendsEquals, rGenericParameterExtends, rGenericParameterEquals, rGenericParameterIdentifier, rGenericParameter  parser
	rGenericParameterList, rGenericParameters, rGenericCallArgumentList, rGenericCallArguments, rGenericCall, rOptionalSemiColon       parser
	rKeywordString, rKeywordNumber, rKeywordBoolean, rKeywordUndefined, rKeywordNull, rKeywordInteger, rKeywordBigInt, rKeywordUnknown parser
	rKeywordAny, rKeywordObject, rKeywordNever, rKeywordSymbol, rKeywordVoid, rKeywordThis                                             parser
	rTemplateInterpolate, rTemplateSpan, rTemplateBody, rTemplateLiteralTypes, rTemplateLiteral, rDependent                            parser
	rLiteralBigInt, rLiteralBoolean, rLiteralNumber, rLiteralString, rKeyOf, rIndexArray, rExtends, rBase, rWith, rFactor              parser
	rExprTermTail, rExprTerm, rExprTail, rExpr, rExprReadonly, rExprPipe, rGenericType, rInferType, rType                              parser
	rPropertyKeyNumber, rPropertyKeyIdent, rPropertyKeyQuoted, rPropertyKeyIndex, rPropertyKey, rReadonly, rOptional, rProperty        parser
	rPropertyDelimiter, rPropertyList, rProperties, rObject, rElementNamed, rElementBase, rElement, rElementList, rTuple               parser
	rParameterBase, rParameter, rParameterList, rFunction, rConstructor, rMappedReadonly, rMappedOptional, rMappedAs, rMapped          parser
	rReference, rWithValue, rWithObject, rWithArray, rWithProperty                                                                     parser
	rPatternBase, rPatternGroup, rPatternUnion, rPatternTerm, rPatternBody, rPattern                                                   parser
	rInterfaceDeclarationGeneric, rInterfaceDeclaration, rTypeAliasDeclarationGeneric, rTypeAliasDeclaration, rModuleDeclaration       parser
	rModule, rScript                                                                                                                   parser
)

func C(s string) parser { return tokConst(s) }

func keyword(word string, build func() any) parser {
	return mapped(C(word), func(any) any { return build() })
}

// grammar builds the parsers on first use: the combinator closures are about 2 KB the pig binary must not allocate at startup.
var grammarOnce sync.Once

func grammar() { grammarOnce.Do(buildGrammar) }

func buildGrammar() {
	typ := ref(&rType)
	listRule := func(item parser, delim parser, m func(any) any) parser {
		// item (delim item)* delim?  |  empty
		return mapped(alt(seq(item, star(seq(delim, item)), alt(wrap(delim), empty)), empty), m)
	}
	commaList := func(item parser) parser {
		return mapped(alt(seq(item, star(seq(C(","), item)), alt(wrap(C(",")), empty)), empty), delimited)
	}
	_ = listRule

	rGenericParameterExtendsEquals = mapped(seq(tokIdent, C("extends"), typ, C("="), typ), func(in any) any { return tParameter(idx(in, 0), idx(in, 2), idx(in, 4)) })
	rGenericParameterExtends = mapped(seq(tokIdent, C("extends"), typ), func(in any) any { return tParameter(idx(in, 0), idx(in, 2), idx(in, 2)) })
	rGenericParameterEquals = mapped(seq(tokIdent, C("="), typ), func(in any) any { return tParameter(idx(in, 0), tUnknown(nil), idx(in, 2)) })
	rGenericParameterIdentifier = mapped(tokIdent, func(in any) any { return tParameter(in, tUnknown(nil), tUnknown(nil)) })
	rGenericParameter = alt(rGenericParameterExtendsEquals, rGenericParameterExtends, rGenericParameterEquals, rGenericParameterIdentifier)
	rGenericParameterList = commaList(ref(&rGenericParameter))
	rGenericParameters = mapped(seq(C("<"), rGenericParameterList, C(">")), func(in any) any { return idx(in, 1) })
	rGenericCallArgumentList = commaList(typ)
	rGenericCallArguments = mapped(seq(C("<"), rGenericCallArgumentList, C(">")), func(in any) any { return idx(in, 1) })
	rGenericCall = mapped(seq(tokIdent, rGenericCallArguments), func(in any) any { return intrinsicOrCall(idx(in, 0), idx(in, 1)) })
	rOptionalSemiColon = mapped(alt(wrap(C(";")), empty), func(any) any { return nil })

	rKeywordString = keyword("string", func() any { return tString(nil) })
	rKeywordNumber = keyword("number", func() any { return tNumber(nil) })
	rKeywordBoolean = keyword("boolean", func() any { return tBoolean(nil) })
	rKeywordUndefined = keyword("undefined", func() any { return tUndefined(nil) })
	rKeywordNull = keyword("null", func() any { return tNull(nil) })
	rKeywordInteger = keyword("integer", func() any { return tInteger(nil) })
	rKeywordBigInt = keyword("bigint", func() any { return tBigInt(nil) })
	rKeywordUnknown = keyword("unknown", func() any { return tUnknown(nil) })
	rKeywordAny = keyword("any", func() any { return tAny(nil) })
	rKeywordObject = keyword("object", func() any { return tObject(NewObj(), nil) })
	rKeywordNever = keyword("never", func() any { return tNever(nil) })
	rKeywordSymbol = keyword("symbol", func() any { return tSymbol(nil) })
	rKeywordVoid = keyword("void", func() any { return tVoid(nil) })
	rKeywordThis = keyword("this", func() any { return tThis(nil) })

	rTemplateInterpolate = mapped(seq(C("${"), typ, C("}")), func(in any) any { return idx(in, 1) })
	rTemplateSpan = mapped(func(in string) pr { return until([]string{"${", "`"}, in) }, func(in any) any { return tLiteral(in, nil) })
	rTemplateBody = mapped(alt(seq(rTemplateSpan, rTemplateInterpolate, ref(&rTemplateBody)), wrap(rTemplateSpan), wrap(rTemplateSpan)), func(in any) any {
		if lengthOf(in) == 3 {
			return append([]any{idx(in, 0), idx(in, 1)}, asArray(idx(in, 2), "length")...)
		}
		return []any{idx(in, 0)}
	})
	rTemplateLiteralTypes = mapped(seq(C("`"), rTemplateBody, C("`")), func(in any) any { return idx(in, 1) })
	rTemplateLiteral = mapped(rTemplateLiteralTypes, func(in any) any { return tDeferred("TemplateLiteral", []any{in}, NewObj()) })
	rDependent = mapped(alt(seq(C("if"), typ, C("then"), typ, C("else"), typ), seq(C("if"), typ, C("then"), typ)), func(in any) any {
		if lengthOf(in) == 6 {
			return tDependent(idx(in, 1), idx(in, 3), idx(in, 5), nil)
		}
		return tDependent(idx(in, 1), idx(in, 3), tUnknown(nil), nil)
	})
	rLiteralBigInt = mapped(tokBigInt, func(in any) any { return tLiteral(bigFromString(in.(string)), nil) })
	rLiteralBoolean = mapped(alt(C("true"), C("false")), func(in any) any { return tLiteral(in == "true", nil) })
	rLiteralNumber = mapped(tokNumber, func(in any) any { return tLiteral(parseFloatToken(in), nil) })
	rLiteralString = mapped(tokString([]string{"'", "\""}), func(in any) any { return tLiteral(in, nil) })
	rKeyOf = mapped(alt(wrap(C("keyof")), empty), func(in any) any { return lengthOf(in) > 0 })
	rIndexArray = mapped(star(alt(seq(C("["), typ, C("]")), seq(C("["), C("]")))), func(in any) any {
		result := []any{}
		for _, current := range asArray(in, "reduce") {
			if lengthOf(current) == 3 {
				result = append(result, []any{idx(current, 1)})
			} else {
				result = append(result, []any{})
			}
		}
		return result
	})
	rExtends = mapped(alt(seq(C("extends"), typ, C("?"), typ, C(":"), typ), empty), func(in any) any {
		if lengthOf(in) == 6 {
			return []any{idx(in, 1), idx(in, 3), idx(in, 5)}
		}
		return []any{}
	})
	rBase = mapped(alt(seq(C("("), typ, C(")")), rKeywordString, rKeywordNumber, rKeywordBoolean, rKeywordUndefined, rKeywordNull, rKeywordInteger, rKeywordBigInt,
		rKeywordUnknown, rKeywordAny, rKeywordObject, rKeywordNever, rKeywordSymbol, rKeywordVoid, rKeywordThis, rLiteralBigInt, rLiteralBoolean, rLiteralNumber,
		rLiteralString, rTemplateLiteral, rDependent, ref(&rObject), ref(&rTuple), ref(&rConstructor), ref(&rFunction), ref(&rMapped), rGenericCall, ref(&rReference)),
		func(in any) any {
			if isArray(in) && lengthOf(in) == 3 {
				return idx(in, 1)
			}
			return in
		})
	rWith = mapped(alt(seq(C("with"), ref(&rWithObject)), empty), func(in any) any {
		if lengthOf(in) == 2 {
			return idx(in, 1)
		}
		return []any{}
	})
	rFactor = mapped(seq(rKeyOf, rBase, rIndexArray, rExtends, rWith), func(in any) any {
		keyOf, t, indexArray, extend, withClause := idx(in, 0), idx(in, 1), idx(in, 2), idx(in, 3), idx(in, 4)
		if truthy(keyOf) {
			return factorWith(factorExtends(deferred1("KeyOf", factorIndexArray(t, indexArray)), extend), withClause)
		}
		return factorWith(factorExtends(factorIndexArray(t, indexArray), extend), withClause)
	})
	rExprTermTail = mapped(alt(seq(C("&"), rFactor, ref(&rExprTermTail)), empty), identity)
	rExprTerm = mapped(seq(rFactor, rExprTermTail), func(in any) any { return exprBinary(idx(in, 0), idx(in, 1)) })
	rExprTail = mapped(alt(seq(C("|"), rExprTerm, ref(&rExprTail)), empty), identity)
	rExpr = mapped(seq(rExprTerm, rExprTail), func(in any) any { return exprBinary(idx(in, 0), idx(in, 1)) })
	rExprReadonly = mapped(seq(C("readonly"), rExpr), func(in any) any { return deferred1("AddImmutable", idx(in, 1)) })
	rExprPipe = mapped(seq(C("|"), rExpr), func(in any) any { return idx(in, 1) })
	rGenericType = mapped(seq(rGenericParameters, C("="), typ), func(in any) any { return tGeneric(idx(in, 0), idx(in, 2)) })
	rInferType = mapped(alt(seq(C("infer"), tokIdent, C("extends"), rExpr), seq(C("infer"), tokIdent)), func(in any) any {
		switch lengthOf(in) {
		case 4:
			return tInfer(idx(in, 1), idx(in, 3))
		case 2:
			return tInfer(idx(in, 1), tUnknown(nil))
		}
		unreachable()
		return nil
	})
	rType = mapped(alt(rInferType, rExprPipe, rExprReadonly, rExpr), identity)

	rPropertyKeyNumber = mapped(tokNumber, func(in any) any { return toStringValue(in) })
	rPropertyKeyIdent = tokIdent
	rPropertyKeyQuoted = tokString([]string{"'", "\""})
	rPropertyKeyIndex = mapped(seq(C("["), tokIdent, C(":"), alt(rKeywordInteger, rKeywordNumber, rKeywordString, rKeywordSymbol), C("]")), func(in any) any {
		k := idx(in, 3)
		switch {
		case isInteger(k):
			return integerKey
		case isNumberType(k):
			return numberKey
		case isSymbol(k):
			return stringKey
		case isStringType(k):
			return stringKey
		}
		unreachable()
		return nil
	})
	rPropertyKey = alt(rPropertyKeyNumber, rPropertyKeyIdent, rPropertyKeyQuoted, rPropertyKeyIndex)
	rReadonly = mapped(alt(wrap(C("readonly")), empty), func(in any) any { return lengthOf(in) > 0 })
	rOptional = mapped(alt(wrap(C("?")), empty), func(in any) any { return lengthOf(in) > 0 })
	rProperty = mapped(seq(rReadonly, rPropertyKey, rOptional, C(":"), typ), func(in any) any {
		isReadonlyP, key, isOptionalP, t := idx(in, 0).(bool), idx(in, 1), idx(in, 2).(bool), idx(in, 4)
		return NewObj(propKey(key), applyModifier(isReadonlyP, isOptionalP, t))
	})
	rPropertyDelimiter = alt(seq(C(","), C("\n")), seq(C(";"), C("\n")), wrap(C(",")), wrap(C(";")), wrap(C("\n")))
	rPropertyList = mapped(alt(seq(rProperty, star(seq(rPropertyDelimiter, rProperty)), alt(wrap(rPropertyDelimiter), empty)), empty), delimited)
	rProperties = mapped(seq(C("{"), rPropertyList, C("}")), func(in any) any { return propertiesReduce(idx(in, 1)) })
	rObject = mapped(rProperties, func(in any) any {
		return tObject(idx(in, 0), objectOptionsFor(idx(in, 1)))
	})

	rElementNamed = mapped(alt(
		seq(tokIdent, C("?"), C(":"), C("readonly"), typ),
		seq(tokIdent, C(":"), C("readonly"), typ),
		seq(tokIdent, C("?"), C(":"), typ),
		seq(tokIdent, C(":"), typ)), func(in any) any {
		switch lengthOf(in) {
		case 5:
			return deferred1("AddReadonly", deferred1("AddOptional", idx(in, 4)))
		case 3:
			return idx(in, 2)
		case 4:
			if idx(in, 2) == "readonly" {
				return deferred1("AddReadonly", idx(in, 3))
			}
			return deferred1("AddOptional", idx(in, 3))
		}
		unreachable()
		return nil
	})
	rElementBase = mapped(alt(rElementNamed, seq(rReadonly, typ, rOptional)), func(in any) any {
		if !isArray(in) || lengthOf(in) != 3 {
			return in
		}
		return applyModifier(idx(in, 0).(bool), idx(in, 2).(bool), idx(in, 1))
	})
	rElement = mapped(alt(seq(C("..."), rElementBase), wrap(rElementBase)), func(in any) any {
		switch lengthOf(in) {
		case 2:
			return tRest(idx(in, 1))
		case 1:
			return idx(in, 0)
		}
		unreachable()
		return nil
	})
	rElementList = commaList(rElement)
	rTuple = mapped(seq(C("["), rElementList, C("]")), func(in any) any { return tTuple(idx(in, 1), nil) })

	rParameterBase = alt(
		mapped(seq(tokIdent, C("?"), C(":"), C("readonly"), typ), func(in any) any { return deferred1("AddReadonly", deferred1("AddOptional", idx(in, 4))) }),
		mapped(seq(tokIdent, C(":"), C("readonly"), typ), func(in any) any { return deferred1("AddReadonly", idx(in, 3)) }),
		mapped(seq(tokIdent, C("?"), C(":"), typ), func(in any) any { return deferred1("AddOptional", idx(in, 3)) }),
		mapped(seq(tokIdent, C(":"), typ), func(in any) any { return idx(in, 2) }))
	rParameter = mapped(alt(seq(C("..."), rParameterBase), wrap(rParameterBase)), func(in any) any {
		switch lengthOf(in) {
		case 2:
			return tRest(idx(in, 1))
		case 1:
			return idx(in, 0)
		}
		unreachable()
		return nil
	})
	rParameterList = commaList(rParameter)
	rFunction = mapped(seq(C("("), rParameterList, C(")"), C("=>"), typ), func(in any) any { return tFunction(idx(in, 1), idx(in, 4), nil) })
	rConstructor = mapped(seq(C("new"), C("("), rParameterList, C(")"), C("=>"), typ), func(in any) any { return tConstructor(idx(in, 2), idx(in, 5), nil) })
	rMappedReadonly = mapped(alt(seq(C("+"), C("readonly")), seq(C("-"), C("readonly")), wrap(C("readonly")), empty), func(in any) any { return mappedModifier(in, "readonly") })
	rMappedOptional = mapped(alt(seq(C("+"), C("?")), seq(C("-"), C("?")), wrap(C("?")), empty), func(in any) any { return mappedModifier(in, "?") })
	rMappedAs = mapped(alt(seq(C("as"), typ), empty), func(in any) any {
		if lengthOf(in) == 2 {
			return []any{idx(in, 1)}
		}
		return []any{}
	})
	rMapped = mapped(seq(C("{"), rMappedReadonly, C("["), tokIdent, C("in"), typ, rMappedAs, C("]"), rMappedOptional, C(":"), typ, rOptionalSemiColon, C("}")), func(in any) any {
		property := applyMappedModifier(idx(in, 1), "AddReadonly", "RemoveReadonly", applyMappedModifier(idx(in, 8), "AddOptional", "RemoveOptional", idx(in, 10)))
		as := idx(in, 6)
		if isArray(as) && lengthOf(as) == 1 {
			return tDeferred("Mapped", []any{tIdentifier(idx(in, 3)), idx(in, 5), idx(as, 0), property}, NewObj())
		}
		return tDeferred("Mapped", []any{tIdentifier(idx(in, 3)), idx(in, 5), tRef(idx(in, 3), nil), property}, NewObj())
	})
	rReference = mapped(tokIdent, func(in any) any { return tRef(in, nil) })

	rWithProperty = mapped(seq(rPropertyKey, C(":"), ref(&rWithValue)), func(in any) any { return NewObj(propKey(idx(in, 0)), idx(in, 2)) })
	rWithObject = mapped(seq(C("{"), mapped(alt(seq(rWithProperty, star(seq(rPropertyDelimiter, rWithProperty)), alt(wrap(rPropertyDelimiter), empty)), empty), delimited), C("}")), func(in any) any {
		var result any = NewObj()
		for _, left := range asArray(idx(in, 1), "reduce") {
			result = Assign(result, left)
		}
		return result
	})
	rWithArray = mapped(seq(C("["), commaList(ref(&rWithValue)), C("]")), func(in any) any { return idx(in, 1) })
	rWithValue = alt(
		mapped(tokBigInt, func(in any) any { return bigFromString(in.(string)) }),
		mapped(tokNumber, func(in any) any { return parseFloatToken(in) }),
		mapped(alt(C("true"), C("false")), func(in any) any { return in == "true" }),
		tokString([]string{"\"", "'"}),
		mapped(C("null"), func(any) any { return nil }),
		mapped(C("undefined"), func(any) any { return Undef }),
		rWithObject, rWithArray)

	const (
		pBigInt  = "-?(?:0|[1-9][0-9]*)n"
		pString  = ".*"
		pNumber  = "-?(?:0|[1-9][0-9]*)(?:\\.[0-9]+)?"
		pInteger = "-?(?:0|[1-9][0-9]*)"
		pNever   = "(?!)"
	)
	rPatternBase = alt(
		mapped(C(pBigInt), func(any) any { return tBigInt(nil) }),
		mapped(C(pString), func(any) any { return tString(nil) }),
		mapped(C(pNumber), func(any) any { return tNumber(nil) }),
		mapped(C(pInteger), func(any) any { return tInteger(nil) }),
		mapped(C(pNever), func(any) any { return tNever(nil) }),
		ref(&rPatternGroup),
		mapped(func(in string) pr {
			return until1([]string{pBigInt, pString, pNumber, pInteger, pNever, "(", ")", "$", "|"}, in)
		}, func(in any) any { return tLiteral(in, nil) }))
	rPatternGroup = mapped(seq(C("("), ref(&rPatternBody), C(")")), func(in any) any { return tUnion(idx(in, 1), nil) })
	rPatternUnion = mapped(alt(seq(ref(&rPatternTerm), C("|"), ref(&rPatternUnion)), wrap(ref(&rPatternTerm)), empty), func(in any) any {
		switch lengthOf(in) {
		case 3:
			return append(slicesCloneAny(asArray(idx(in, 0), "length")), asArray(idx(in, 2), "length")...)
		case 1:
			return slicesCloneAny(asArray(idx(in, 0), "length"))
		}
		return []any{}
	})
	rPatternTerm = mapped(seq(rPatternBase, ref(&rPatternBody)), func(in any) any {
		return append([]any{idx(in, 0)}, asArray(idx(in, 1), "length")...)
	})
	rPatternBody = alt(rPatternUnion, rPatternTerm)
	rPattern = mapped(seq(C("^"), rPatternBody, C("$")), func(in any) any { return idx(in, 1) })

	declList := func(m func(any) any, item parser, delim parser) parser {
		return mapped(alt(seq(item, star(seq(delim, item)), alt(wrap(delim), empty)), empty), m)
	}
	_ = declList
	rInterfaceHeritage := mapped(alt(seq(C("extends"), commaList(typ)), empty), func(in any) any {
		if lengthOf(in) == 2 {
			return idx(in, 1)
		}
		return []any{}
	})
	rInterfaceDeclarationGeneric = mapped(seq(C("interface"), tokIdent, rGenericParameters, rInterfaceHeritage, rProperties), func(in any) any {
		parameters, heritage := idx(in, 2), idx(in, 3)
		properties, patternProperties := idx(idx(in, 4), 0), idx(idx(in, 4), 1)
		return NewObj(propKey(idx(in, 1)), tGeneric(parameters, tDeferred("Interface", []any{heritage, properties}, objectOptionsFor(patternProperties))))
	})
	rInterfaceDeclaration = mapped(seq(C("interface"), tokIdent, rInterfaceHeritage, rProperties), func(in any) any {
		heritage := idx(in, 2)
		properties, patternProperties := idx(idx(in, 3), 0), idx(idx(in, 3), 1)
		return NewObj(propKey(idx(in, 1)), tDeferred("Interface", []any{heritage, properties}, objectOptionsFor(patternProperties)))
	})
	rTypeAliasDeclarationGeneric = mapped(seq(C("type"), tokIdent, rGenericParameters, C("="), typ), func(in any) any {
		return NewObj(propKey(idx(in, 1)), tGeneric(idx(in, 2), idx(in, 4)))
	})
	rTypeAliasDeclaration = mapped(seq(C("type"), tokIdent, C("="), typ), func(in any) any { return NewObj(propKey(idx(in, 1)), idx(in, 3)) })
	exportKeyword := mapped(alt(wrap(C("export")), empty), func(any) any { return nil })
	rModuleDeclaration = mapped(seq(exportKeyword, alt(rInterfaceDeclarationGeneric, rInterfaceDeclaration, rTypeAliasDeclarationGeneric, rTypeAliasDeclaration), rOptionalSemiColon), func(in any) any { return idx(in, 1) })
	moduleDelimiter := alt(seq(C(";"), C("\n")), wrap(C(";")), wrap(C("\n")))
	rModule = mapped(seq(rModuleDeclaration, mapped(alt(seq(rModuleDeclaration, star(seq(moduleDelimiter, rModuleDeclaration)), alt(wrap(moduleDelimiter), empty)), empty), delimited)), func(in any) any {
		declaration, list := idx(in, 0), idx(in, 1)
		return tDeferred("Module", []any{Assign(declaration, propertiesReduce(list)[0])}, NewObj())
	})
	rScript = alt(rModule, rGenericType, typ)
}

func slicesCloneAny(a []any) []any { return append([]any(nil), a...) }

// scriptParse is Parser.Script(input).
func scriptParse(input string) pr {
	grammar()
	return rScript(input)
}

// scriptPattern is Parser.Pattern(input): a template literal's `^...$` pattern as the list of types it stands for.
func scriptPattern(input string) pr {
	grammar()
	return rPattern(input)
}

// scriptTemplateLiteralTypes is Parser.TemplateLiteralTypes(input).
func scriptTemplateLiteralTypes(input string) pr {
	grammar()
	return rTemplateLiteralTypes(input)
}
