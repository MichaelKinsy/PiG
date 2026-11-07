#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Mutation check of the rewrite's decisions: each mutant changes one decision in rewrite.go, rewrite_analysis.go or rewrite_cast.go, the package's rewrite tests run, and the mutant
must be killed by a test failure (not by a build error). Prints each mutant and a total; exits 1 when one survives.
usage: mutate.py PKGDIR   (PKGDIR = piglets/pig-bolt/extensions/boltruntime; run where Go is; PIG_AOT_TEST_BUN, if set, adds the parse check to the tests)"""
import subprocess, sys, re, os
pkg = sys.argv[1]
files = [os.path.join(pkg, n) for n in ("rewrite.go", "rewrite_analysis.go", "rewrite_cast.go")]
origs = {f: open(f).read() for f in files}
M = [
 ("line comments are skipped", "case c == '/' && next == '/':", "case false:"),
 ("block comments are skipped", "case c == '/' && next == '*':", "case false:"),
 ("quoted strings are one token", "case c == '\\'' || c == '\"':", "case false:"),
 ("template text is not code", "case c == '`':", "case false:"),
 ("a template substitution closes back into the text", "case haveBrace && brace.kind == brTemplate:", "case false && brace.kind == brTemplate:"),
 ("regular expression literals", "case c == '/' && l.expressionMayBegin() && l.regexpAhead():", "case false:"),
 ("a regex may follow a block's }", "expr: !haveBrace || brace.kind != brObject}", "expr: false}"),
 ("a regex does not follow an object literal's }", "expr: !haveBrace || brace.kind != brObject}", "expr: true}"),
 ("a regex follows the ) of a condition", "l.parens = append(l.parens, l.prev.kind == pvWord && beforeCondition[l.prev.text])", "l.parens = append(l.parens, false)"),
 ("a word after a dot is a property", "t.property = l.afterDot(len(l.toks) - 1)", "t.property = false"),
 ("a spread is not a property access", "if k >= 3 && isAdjacentDot(t[k-2], t[k-1]) && isAdjacentDot(t[k-3], t[k-2]) {", "if false {"),
 ("const/let/var/function/class declare", "declarationKeywords[toks[i-1].text]", "false"),
 ("an assignment declares", "if a.isPunct(i+1, \"=\") && (!a.isPunct(i+2, \"=\") || toks[i+2].start != toks[i+1].end) {", "if a.isPunct(i+1, \"=\") {"),
 ("a call's argument is not a parameter", "&& a.parameterList(enclosing)", ""),
 ("a condition's parenthesis is not a parameter list", "conditionKeywords[a.toks[open-1].text]", "false"),
 ("an object literal's shorthand is not a binding", "&& a.bindingPattern(enclosing)", ""),
 ("a nested pattern is a binding", "return nested && a.bindingPattern(parent)", "return nested && a.bindingPattern(parent) && false"),
 ("an import binds its names", "a.importBindings(i)\n\t\t\tcontinue", "continue"),
 ("a method is not a call", "return a.isPunct(closeIdx+1, \"{\") || a.isPunct(closeIdx+1, \":\") && member", "return (a.isPunct(closeIdx+1, \"{\") || a.isPunct(closeIdx+1, \":\") && member) && false"),
 ("a modifier makes a method", "if p.kind == tkWord && methodModifiers[p.text] || a.isPunct(i-1, \"*\") {", "if (p.kind == tkWord && methodModifiers[p.text] || a.isPunct(i-1, \"*\")) && false {"),
 ("a string argument is the bundler's", "if a.toks[open+1].kind != tkString {", "if true {"),
 ("a name followed by ( is not rewritten", "a.isPunct(i+1, \"(\") || a.isMemberName(i)", "a.isMemberName(i)"),
 ("an object key is a name", "return a.isPunct(next, \":\") || (a.isPunct(next, \"?\") || a.isPunct(next, \"!\")) && a.isPunct(next+1, \":\")", "return (a.isPunct(next, \"?\") || a.isPunct(next, \"!\")) && a.isPunct(next+1, \":\")"),
 ("a shorthand keeps its name", "value = t.text + \": \" + value", "value = value"),
 ("JSX is scanned", "case c == '<' && l.jsx && l.expressionMayBegin() && l.jsxBegins():", "case false:"),
 ("a generic arrow is not JSX", "if j < len(src) && src[j] == ',' {", "if false {"),
 ("line breaks of a replaced span stay", "out.WriteString(strings.Repeat(\"\\n\", e.breaks))", ""),
 ("import.meta used as a value is reported", "unpinned = \"import.meta\"\n\t\t\t\t\tcontinue", "continue"),
 ("a TypeScript cast of import.meta", "if a.castImportMeta(i) {", "if false {"),
 ("the cast's ( is no call's", "return before.text != \")\" && before.text != \"]\" && before.text != \"}\"", "return true"),
 ("code inserted into a script escapes / and <", "case '<', '>', '/', '\\u2028', '\\u2029':", "case '<', '>', '\\u2028', '\\u2029':"),
 ("file URLs encode ~", "\"-._!$&'()*+,/:;=@\"", "\"-._~!$&'()*+,/:;=@\""),
 ("a UTF-8 path is required", "if !utf8.ValidString(file) && (unpinned != \"\" || len(edits) > 0) {", "if false {"),
 ("require.resolve", "if a.isPunct(i+1, \".\") && a.isWord(i+2, \"resolve\") {", "if false {"),
 ("a JSX container's braces are not an object", "a.toks[brace].jsxBrace ||", ""),
 ("a postfix ++ or -- is a value", "postfix := !l.expressionMayBegin()", "postfix := false"),
 ("++ and -- are one token", "case (c == '+' || c == '-') && next == c:", "case false:"),
 ("a non-null assertion is a value", "case c == '!' && !l.expressionMayBegin():", "case false:"),
 ("a member's name is a value", "if t.property {\n\t\t\t\t// `o.return / 2`", "if false {\n\t\t\t\t// `o.return / 2`"),
 ("CR ends a line", "case c == '\\n' || c == '\\r':\n\t\treturn 1", "case c == '\\n':\n\t\treturn 1"),
 ("U+2028 and U+2029 end a line", "(s[i+2] == 0xa8 || s[i+2] == 0xa9)", "s[i+2] == 0xa8"),
 ("a backslash before CR LF continues a string", "if l.i+1 < len(l.src) && l.src[l.i] == '\\r' && l.src[l.i+1] == '\\n' {", "if false {"),
 ("CR ends a string", "case '\\n', '\\r':\n\t\t\treturn", "case '\\n':\n\t\t\treturn"),
 ("a line terminator ends a regex", "if lineTerminatorWidth(src, j) > 0 {", "if src[j] == '\\n' {"),
 ("a hashbang ends at a line terminator", "l.i = lineEnd(src, 0)", "l.i = strings.IndexByte(src, '\\n') + 0*len(src)"),
 ("JavaScript files are scanned for JSX", "case \".ts\", \".mts\", \".cts\":", "case \".ts\", \".mts\", \".cts\", \".js\", \".mjs\", \".cjs\":"),
 ("the name after typeof is left", "if i > 0 && a.isWord(i-1, \"typeof\") && !toks[i-1].property {", "if false {"),
 ("a typeof annotation is no declaration", "if a.isWord(i-1, \"typeof\") && !toks[i-1].property {\n\t\treturn false\n\t}", ""),
 ("a class's members are names", "case bodyClass, bodyEnum, bodyInterface:\n\t\treturn true", "case bodyEnum, bodyInterface:\n\t\treturn true"),
 ("an enum's members are names", "case bodyClass, bodyEnum, bodyInterface:\n\t\treturn true", "case bodyClass, bodyInterface:\n\t\treturn true"),
 ("an interface's members are names", "case bodyClass, bodyEnum, bodyInterface:\n\t\treturn true", "case bodyClass, bodyEnum:\n\t\treturn true"),
 ("a member ends at the end of its line", "starts = ends && lineEnd(a.src, prev.end) < a.toks[k+1].start", "starts = ends && lineEnd(a.src, prev.end) < a.toks[k+1].start && false"),
 ("a member's modifiers come before its name", "for k >= 0 && a.toks[k].kind == tkWord && memberModifiers[a.toks[k].text] {", "for false {"),
 ("a type literal's optional member", "return a.isPunct(next, \":\") || (a.isPunct(next, \"?\") || a.isPunct(next, \"!\")) && a.isPunct(next+1, \":\")", "return a.isPunct(next, \":\")"),
 ("a class head's type arguments are skipped", "\t\t\tcase \">\":\n\t\t\t\tdepth := 0", "\t\t\tcase \"@\":\n\t\t\t\tdepth := 0"),
 ("a colon ends a class head's search", "case \";\", \"{\", \"}\", \":\", \"=\", \"=>\":", "case \";\", \"{\", \"}\", \"=\", \"=>\":"),
 ("a member is no declaration", "if a.isMemberName(i) {\n\t\treturn false\n\t}", ""),
 ("an import's list is a module's names", "case a.isWord(j, \"import\") && !a.toks[j].property:\n\t\t\treturn true", "case false:\n\t\t\treturn true"),
 ("an export list is a module's names", "if a.isWord(i-1, \"export\") || a.isWord(i-1, \"type\") && a.isWord(i-2, \"export\") {", "if false {"),
 ("a namespace re-export names a module's name", "a.isWord(i-1, \"as\") && a.isPunct(i-2, \"*\")", "false"),
 ("a parameter property declares", "for k >= 0 && toks[k].kind == tkWord && parameterModifiers[toks[k].text] {", "for false {"),
 ("an import binds the name after as", "locationNames[t.text] && !a.isWord(j+1, \"as\")", "locationNames[t.text]"),
]
killed = survived = 0
try:
    for name, old, new in M:
        target = next((f for f in files if old in origs[f]), None)
        if target is None:
            print(f"BAD MUTANT (pattern not found): {name}"); survived += 1; continue
        open(target, "w").write(origs[target].replace(old, new, 1))
        r = subprocess.run(["go", "test", ".", "-run", "Rewrite|FileURL|Scan", "-count=1"], cwd=pkg, capture_output=True, text=True, errors="replace", env=dict(os.environ, GOWORK="off"))
        open(target, "w").write(origs[target])
        fails = sorted(set(re.findall(r"^\s*--- FAIL: (\S+)", r.stdout, re.M)))
        if r.returncode == 0:
            survived += 1; print(f"SURVIVED  {name}")
        elif not fails:
            survived += 1; print(f"NOT A TEST FAILURE (build error?)  {name}: {r.stdout[-200:]}")
        else:
            killed += 1; print(f"KILLED    {name}  [{len(fails)}] {', '.join(fails[:2])}")
finally:
    for f, text in origs.items(): open(f, "w").write(text)
print(f"{killed} killed, {survived} survived of {len(M)}")
sys.exit(1 if survived else 0)
