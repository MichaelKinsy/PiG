"""session-replaced-2860.py runs the bodies of Pi's 2860-replaced-session-context.test.ts cases as Python SDK commands.

Each line goes to the file named by PIG_TEST_2860_LOG; process ids name the extension instances, because each Session's extensions run in their own process here."""
import json
import os

import pig_sdk


def _log(line):
    with open(os.environ["PIG_TEST_2860_LOG"], "a") as report:
        report.write(line + "\n")


def _text(content):
    if isinstance(content, str):
        return content
    return "".join(part.get("text", "") for part in content or [] if part.get("type") == "text")


def _conversation(manager):
    rows = []
    for entry in manager.get_branch() or []:
        message = entry.get("message") or {}
        if entry.get("type") != "message" or message.get("role") == "system":
            continue
        rows.append(f"{message.get('role')}:{_text(message.get('content'))}")
    return "|".join(rows)


def _marks():
    try:
        with open(os.environ["PIG_TEST_2860_MARKS"]) as marks:
            return json.load(marks)
    except OSError:
        return {}


def new_extension() -> pig_sdk.Extension:
    ext = pig_sdk.Extension("session-replaced-2860")
    pid = os.getpid()
    ext.on_event("session_start", lambda ctx, data: _log(f"start:{pid}"))
    ext.on_event("session_shutdown", lambda ctx, data: _log(f"shutdown:{pid}"))

    def repro(ctx, args):
        old_ctx = ctx
        old_session_file = ctx.session_manager.get_session_file()

        def with_session(replaced):
            _log(f"with:{pid}")
            replacement = replaced.session_manager.get_session_file()
            _log(f"replacement:{str(replacement is not None and replacement != old_session_file).lower()}")
            try:
                old_ctx.session_manager.get_session_file()
                _log("staleCtx:false")
            except Exception:
                _log("staleCtx:true")
            try:
                old_ctx.send_user_message("stale message")
                _log("stalePi:false")
            except Exception:
                _log("stalePi:true")
            replaced.send_user_message("reply with exactly: hello reply")
            _log(f"idle:{str(replaced.is_idle()).lower()}")
            # Pi's `await sleep()` works in a withSession callback: the SDK's yield helper must answer on the replacement route too.
            try:
                replaced.sleep(0.001)
                _log("yield:ok")
            except Exception as error:
                _log(f"yield:{error}")
            _log(f"model:{replaced.model}")
            _log("conversation:" + _conversation(replaced.session_manager))

        ctx.new_session({"parentSession": old_session_file, "withSession": with_session})

    def throw_it(ctx, args):
        thrown = RuntimeError("callback failed")

        def with_session(replaced):
            raise thrown

        try:
            ctx.new_session({"withSession": with_session})
            _log("caught:none")
        except Exception as error:
            _log(f"caught:{error}" if error is thrown else f"caught:other:{error}")

    def keep_it(ctx, args):
        # A context kept past its callback must not block the extension: the command still returns.
        kept = []
        ctx.new_session({"withSession": kept.append})
        for read in (lambda: kept[0].session_manager.get_session_file(), lambda: kept[0].is_idle(), lambda: kept[0].send_user_message("kept message")):
            try:
                read()
            except Exception:
                pass
        _log("kept:returned")

    def fork_it(ctx, args):
        leaf_id = ctx.session_manager.get_leaf_id()
        if not leaf_id:
            raise RuntimeError("Missing leaf id")

        def with_session(replaced):
            replaced.send_user_message("reply with exactly: fork reply")
            _log("conversation:" + _conversation(replaced.session_manager))

        ctx.fork(leaf_id, {"position": "at", "withSession": with_session})

    def expand_it(ctx, args):
        # types.ts:463-473: the replacement context's sendUserMessage takes expandPromptTemplates; true expands the prompt template, false and no option send the text as typed.
        def with_session(replaced):
            replaced.send_user_message("/echo Alice", expand_prompt_templates=True)
            replaced.send_user_message("/echo reply with exactly: Bob", expand_prompt_templates=False)
            replaced.send_user_message("/echo reply with exactly: Carol")
            _log("conversation:" + _conversation(replaced.session_manager))

        ctx.new_session({"withSession": with_session})

    def mark(ctx, name):
        marks = _marks()
        marks[name] = ctx.session_manager.get_session_file()
        with open(os.environ["PIG_TEST_2860_MARKS"], "w") as out:
            json.dump(marks, out)

    def switch_it(ctx, args):
        target = _marks()["target"]

        def with_session(replaced):
            replaced.send_user_message("reply with exactly: switch reply")
            _log(f"switched:{str(replaced.session_manager.get_session_file() == target).lower()}")
            _log("conversation:" + _conversation(replaced.session_manager))

        ctx.switch_session(target, {"withSession": with_session})

    def seed_it(ctx, args):
        # types.ts:411, agent-session-runtime.ts:254-257: setup seeds the replacement Session through its SessionManager before withSession runs.
        def setup(manager):
            ids = [manager.append_custom_message_entry("seed-msg", "from setup", True), manager.append_session_info("seeded-session")]
            _log(f"setup:{all(isinstance(i, str) and i for i in ids) and len(set(ids)) == len(ids)}".lower())

        def with_session(replaced):
            _log(f"seedName:{replaced.session_manager.get_session_name()}")
            entries = [e for e in replaced.session_manager.get_entries() if e.get("type") == "custom_message"]
            _log("seedEntry:" + "|".join(f"{e.get('customType')}={e.get('content')}" for e in entries))

        ctx.new_session({"setup": setup, "withSession": with_session})

    def getters(ctx, args):
        # D30: what a captured context's state getters answer after the replacement. Each outcome is one line.
        old_ctx = ctx

        def with_session(replaced):
            def probe(name, read):
                try:
                    _log(f"getter:{name}={read()}")
                except Exception as error:
                    _log(f"getter:{name}=raises:{error}")

            probe("cwd", lambda: "nonempty" if old_ctx.cwd else "empty")
            probe("mode", lambda: old_ctx.mode)
            probe("hasUI", lambda: str(old_ctx.has_ui()).lower())
            probe("model", lambda: old_ctx.model)
            probe("theme", lambda: type(old_ctx.theme).__name__)
            probe("branch", lambda: _conversation(old_ctx.session_manager))
            probe("entries", lambda: str(len([e for e in old_ctx.session_manager.get_entries() if e.get("type") == "message"])))
            _log("replacement:" + _conversation(replaced.session_manager))

        ctx.new_session({"withSession": with_session})

    def calls(ctx, args):
        # D30: what each host call from a captured context does after the replacement, and whether it reaches the replacement. One line per call.
        old_ctx = ctx

        def with_session(replaced):
            before = replaced.get_active_tools()

            def report(name, call):
                try:
                    call()
                    _log(f"call:{name}=nil")
                except Exception as error:
                    _log(f"call:{name}=raises:{type(error).__name__}:{error}")

            report("notify", lambda: old_ctx.notify("stale note", "info"))
            report("setStatus", lambda: old_ctx.set_status("stale", "text"))
            report("setActiveTools", lambda: old_ctx.set_active_tools(["read"]))
            report("abort", lambda: old_ctx.abort())
            report("sendUserMessage", lambda: old_ctx.send_user_message("stale message"))
            report("appendEntry", lambda: old_ctx.append_entry("stale-entry", {"a": 1}))
            report("setSessionName", lambda: old_ctx.set_session_name("stale-name"))
            report("setWidgetLines", lambda: old_ctx.set_widget("stale", ["line"]))
            report("setWidgetOptions", lambda: old_ctx.set_widget("stale", ["line"], {"placement": "belowEditor"}))
            after = replaced.get_active_tools()
            custom = len([e for e in replaced.session_manager.get_entries() if e.get("type") == "custom"])
            named = str(replaced.session_manager.get_session_name() == "stale-name").lower()
            _log(f"replacement:toolsChanged={str(before != after).lower()} named={named} customEntries={custom} conversation={_conversation(replaced.session_manager)}")

        ctx.new_session({"withSession": with_session})

    ext.command("calls", "calls", calls)
    ext.command("getters", "getters", getters)
    ext.command("repro", "repro", repro)
    ext.command("seed-it", "seed-it", seed_it)
    ext.command("throw-it", "throw-it", throw_it)
    ext.command("keep-it", "keep-it", keep_it)
    ext.command("fork-it", "fork-it", fork_it)
    ext.command("expand-it", "expand-it", expand_it)
    ext.command("mark", "mark", mark)
    ext.command("new", "new", lambda ctx, args: ctx.new_session())
    ext.command("switch", "switch", lambda ctx, name: ctx.switch_session(_marks()[name]))
    ext.command("switch-it", "switch-it", switch_it)
    return ext
