package plugins

import (
	"math/rand/v2"
	"strings"
)

// cssModuleCases is count CSS modules, the same ones every run: what a differential of the scoping against Pi's own reads
// (`scopeCssModule`, golden in testdata/css-modules-pi.jsonl.gz) and against Node's evaluation of esbuild's mapping module
// compares. The names are the ones that told the two apart: escapes that spell integers (JavaScript orders those keys first,
// ascending), `__proto__` (a literal's prototype setter, dropped by a spread), names with both quote characters (esbuild
// writes those as a template literal), U+2028/U+2029, lone surrogates, tabs, `composes`, `:global`, keyframes, ids,
// counter styles and container names.
func cssModuleCases(count int) []string {
	random := rand.New(rand.NewPCG(0x63737332, 0x6d6f6475))
	cases := make([]string, count)
	for i := range cases {
		cases[i] = cssModuleCase(random)
	}
	return cases
}

// plainClassNames are names that are one identifier: a rule's `composes` may name them.
var plainClassNames = []string{"a", "b", "c", "card", "x1", "foo-bar", "_u", "z", "base", "item", "en-US"}

// oddClassNames are names written with escapes, as CSS source gives them.
var oddClassNames = []string{
	`__proto__`, `constructor`, `toString`, `hasOwnProperty`, `default`, `valueOf`,
	`\31 `, `\30 `, `\32 `, `\31 0`, `\31 00`, `\35 `, `\39 `, `\30 1`, `\31 e3`, `\31 2345678`,
	`\34 294967294`, `\34 294967295`, `\34 294967296`, `\32 147483647`, `\33 6893488147419103232`,
	`a\"\'`, `a\"b`, `a\'b`, `a\\b`, `\"`, `\'`,
	`é`, `caf\E9 `, `\1F600 `, `a\2028 `, `a\2029 b`, `\D800 `, `\DC00 `, `\FFFF `, `\10FFFF `,
	`x\9 y`, `x\A y`, `\{`, `a\{b`, `\31 \32 `, `\61 b`, `\-x`, `a\ b`, `a\,b`, `a\.b`, `a\:b`,
}

func cssModuleCase(random *rand.Rand) string {
	var css strings.Builder
	local := []string{}
	pick := func(from []string) string { return from[random.IntN(len(from))] }
	name := func() string {
		if random.IntN(3) == 0 {
			return pick(oddClassNames)
		}
		return pick(plainClassNames)
	}
	rules := 1 + random.IntN(7)
	for range rules {
		if css.Len() > 0 {
			css.WriteByte(' ')
		}
		switch random.IntN(14) {
		case 0, 1, 2, 3, 4, 5:
			class := name()
			css.WriteString("." + class + "{color:red}")
			if !strings.Contains(class, `\`) {
				local = append(local, class)
			}
		case 6:
			if len(local) > 0 {
				css.WriteString("." + pick(plainClassNames) + "{composes:" + pick(local) + ";color:blue}")
			} else {
				css.WriteString(".a{composes:b}")
			}
		case 7:
			css.WriteString("." + pick(plainClassNames) + "{composes: x y from global;color:blue}")
		case 8:
			css.WriteString(":global(." + pick(plainClassNames) + ") ." + name() + "{color:red}")
		case 9:
			css.WriteString("@keyframes " + pick([]string{"spin", "fade", "__proto__", `\31 `, "constructor"}) + "{from{opacity:0}}")
		case 10:
			css.WriteString("#" + pick([]string{"ident", "id1", "__proto__", "x"}) + "{color:red}")
		case 11:
			css.WriteString("." + name() + ":not(." + name() + "){}")
		case 12:
			css.WriteString("@container " + pick([]string{"card", "box", "__proto__", `\31 `}) + " (min-width:1px){." + name() + "{}}")
		default:
			css.WriteString("@counter-style " + pick([]string{"thumbs", "stars", "__proto__"}) + "{system:cyclic;symbols:x} ." + name() + "{list-style:thumbs}")
		}
	}
	return css.String()
}
