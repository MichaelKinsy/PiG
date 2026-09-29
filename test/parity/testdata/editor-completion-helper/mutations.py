#!/usr/bin/env python3
"""Compile and kill focused source mutations without editing shared production files."""
import json
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parents[4]
out = Path(sys.argv[1]).resolve()
out.mkdir(parents=True, exist_ok=True)
mutations = [
    ("single-pre-undo", "tui/editor.go", 'e.saveHistory()\n\te.lastAction = ""\n\tlines, row, byteCol := e.autocompleteView()\n\tnewLines, row, col :=', 'e.lastAction = ""\n\tlines, row, byteCol := e.autocompleteView()\n\tnewLines, row, col :=', "TestCompletionUndoRestoresPreApplyTextAndCursor"),
    ("async-pre-undo", "tui/editor_async_provider.go", 'e.AutocompleteCancel()\n\te.saveHistory()\n\te.lastAction = ""', 'e.AutocompleteCancel()\n\te.lastAction = ""', "TestCompletionUndoRestoresPreApplyTextAndCursor"),
    ("keyboard-wrap", "tui/editor.go", "c := (e.autocompleteCursor + delta) % n", "c := max(0, min(e.autocompleteCursor+delta, n-1))", "TestCompletionSelectionKeyboardWrapAndMouseClamp"),
    ("unbound-control-p", "tui/editor.go", "case kb.Matches(data, KBSelectUp):", 'case kb.Matches(data, KBSelectUp), data == "\\x10":', "TestCompletionSelectionKeyboardWrapAndMouseClamp"),
    ("mouse-clamp", "tui/editor.go", "e.AutocompleteMove(target - e.autocompleteCursor)", "e.AutocompleteMove(delta + target - target)", "TestCompletionSelectionKeyboardWrapAndMouseClamp"),
    ("cjk-delimiter", "tui/file_autocomplete.go", "last = i + utf8.RuneLen(r) - 1", "last = i", "TestUpstreamEditorCompletionChinesePathPrefixes"),
    ("joined-order", "tui/editor_async_provider.go", "if previous != nil {\n\t\t\t\t<-previous", "if false && previous != nil {\n\t\t\t\t<-previous", "TestOwnedAutocompleteJoinsCancelledPredecessors"),
    ("owner-publication", "tui/editor_async_provider.go", "select {\n\t\t\tcase <-applied:\n\t\t\tcase <-lifetime.Done():\n\t\t\t}", "return", "TestOwnedAutocompleteWaitsForOwnerPublication"),
    ("await-callback", "tui/autocomplete.go", "return command.AwaitArgumentCompletions(prefix)", "return nil, nil", "TestUpstreamEditorCompletionAwaitsSlashCommandArguments|TestUpstreamEditorCompletionIgnoresInvalidArgumentResult"),
    ("node-invalid-shape", "coding/extension/host/subprocess/runtime-node/runtime.mjs", "await this.respond(id, Array.isArray(items) ? items : null);", "await this.respond(id, items);", "TestUpstreamEditorCompletionIgnoresInvalidArgumentResult"),
    ("best-prefix", "tui/editor_autocomplete_request.go", "return max(0, first)", "return 0", "TestUpstreamEditorCompletionArgumentSelection"),
    ("cursor-requery", "tui/editor.go", "defer func() {\n\t\tif e.AutocompleteOpen() {\n\t\t\te.refreshAutocomplete()", "defer func() {\n\t\tif false && e.AutocompleteOpen() {\n\t\t\te.refreshAutocomplete()", "TestUpstreamEditorCompletionRequeriesOnCursorMove"),
    ("abort-active", "tui/editor.go", "e.asyncCancel()\n\t\te.asyncCancel = nil\n\t}\n\tif !e.AutocompleteOpen()", "e.asyncCancel = nil\n\t}\n\tif !e.AutocompleteOpen()", "TestUpstreamEditorCompletionAbortsActiveRequest"),
    ("debounce-required", "tui/editor_async_provider.go", "if !explicit && !force && autocompleteTriggered(", "if false && !explicit && !force && autocompleteTriggered(","TestUpstreamEditorCompletionCJKSymbolDebounce|TestUpstreamEditorCompletionDebouncesWhileTyping"),
    ("debounce-boundary", "tui/editor_async_provider.go", "time.NewTimer(attachmentAutocompleteDebounce)", "time.NewTimer(attachmentAutocompleteDebounce + time.Millisecond)", "TestUpstreamEditorCompletionCJKSymbolDebounce"),
    ("force-option", "tui/editor.go", "e.requestNativeAutocomplete(true, true)", "e.requestNativeAutocomplete(false, true)", "TestUpstreamEditorCompletionCJKPathOnlyOnTab|TestUpstreamEditorCompletionUndoAndForce"),
    ("force-single", "tui/editor_async_provider.go", "if force && explicit && len(result.Items) == 1 {", "if force && explicit && len(result.Items) == 0 {", "TestUpstreamEditorCompletionUndoAndForce"),
    ("force-multiple", "tui/editor_async_provider.go", "if force && explicit && len(result.Items) == 1 {", "if force && explicit && len(result.Items) >= 1 {", "TestUpstreamEditorCompletionForceMultiple"),
    ("force-retention", "tui/editor.go", 'force := e.AutocompleteOpen() && e.autocompleteForced', 'force := false', "TestUpstreamEditorCompletionKeepsForceMode"),
    ("cursor-units", "tui/editor_autocomplete_units.go", "e.setCursorCol(col)", "e.setCursorCol(col + byteCol - col)", "TestUpstreamEditorCompletionChinesePathPrefixes|TestUpstreamEditorCompletionRetriggersCJKDirectoriesAndDeletion"),
    ("accept-no-cancel", "tui/editor.go", 'isSlashName := strings.HasPrefix(prefix, "/")\n\te.AutocompleteCancel()', 'isSlashName := strings.HasPrefix(prefix, "/")', "TestUpstreamEditorCompletionForceMultiple|TestUpstreamEditorCompletionKeepsForceMode|TestUpstreamEditorCompletionCommandWithoutArgumentCompleter"),
    ("accept-no-undo", "tui/editor.go", 'e.saveHistory()\n\te.lastAction = ""\n\tlines, row, byteCol := e.autocompleteView()\n\tnewLines, nl, nc :=', 'e.lastAction = ""\n\tlines, row, byteCol := e.autocompleteView()\n\tnewLines, nl, nc :=', "TestUpstreamEditorCompletionUndoAndForce|TestCompletionUndoRestoresPreApplyTextAndCursor"),
    ("delete-no-refresh", "tui/editor.go", "e.backspace()\n\t\te.refreshAutocomplete()", "e.backspace()", "TestUpstreamEditorCompletionBackspaceSlashToEmpty|TestUpstreamEditorCompletionRetriggersCJKDirectoriesAndDeletion"),
]
# refreshAutocomplete and requestOwnedAutocomplete both cancel the prior query.
# Removing only one is not a behavioral mutation of cancellation on this path.
extra_mutations = {"abort-active": [("tui/editor_async_provider.go", "if e.asyncCancel != nil {\n\t\te.asyncCancel()\n\t}", "if e.asyncCancel != nil {\n\t\te.asyncCancel = nil\n\t}")]}
pair_mutations = {"keyboard-wrap", "unbound-control-p", "await-callback", "node-invalid-shape", "best-prefix", "cursor-requery", "force-option", "force-single", "force-multiple", "force-retention", "accept-no-cancel", "accept-no-undo", "delete-no-refresh"}
pi = subprocess.run(["node", "test/parity/testdata/editor-completion-helper/pi.mjs"], cwd=root, capture_output=True, check=True)
(out / "pi-output.json").write_bytes(pi.stdout)
for name, source, old, new, tests in mutations:
    directory = out / name
    directory.mkdir(exist_ok=True)
    replacements = {}
    for source, old, new in [(source, old, new), *extra_mutations.get(name, [])]:
        source = root / source
        text = source.read_text()
        if text.count(old) != 1:
            raise RuntimeError(f"{name}: mutation target is not unique in {source}")
        replacement = directory / source.name
        replacement.write_text(text.replace(old, new))
        replacements[str(source)] = str(replacement)
    overlay = directory / "overlay.json"
    overlay.write_text(json.dumps({"Replace": replacements}))
    statuses = []
    for phase, pattern in [("compile", "^$"), ("guard", tests)]:
        with (directory / (phase + ".txt")).open("w") as log:
            result = subprocess.run(["go", "test", "-overlay", str(overlay), "./tui", "-count=1", "-run", pattern], cwd=root, stdout=log, stderr=subprocess.STDOUT, check=False)
        statuses.append(result.returncode)
    print(f"{name}: compile={statuses[0]} guard={statuses[1]}", flush=True)
    if statuses != [0, 1]:
        raise RuntimeError(f"{name}: mutation was not compiled and killed")
    if name in pair_mutations:
        pair = subprocess.run(["go", "run", "-overlay", str(overlay), "./test/parity/testdata/editor-completion-helper/pig"], cwd=root, capture_output=True, check=False)
        (directory / "pig-output.txt").write_bytes(pair.stdout)
        (directory / "pig-stderr.txt").write_bytes(pair.stderr)
        equal = pair.returncode == 0 and pair.stdout == pi.stdout
        print(f"{name}: fixture exit={pair.returncode} output_equal={equal}", flush=True)
        if equal:
            raise RuntimeError(f"{name}: canonical fixture failed to expose mutation")
