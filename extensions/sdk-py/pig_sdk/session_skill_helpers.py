"""Pi library helpers over session entries and skills: CURRENT_SESSION_VERSION (core/session-manager.ts:41), VIRTUAL_MODEL_STATE_ENTRY
(core/virtual-models.ts:32), getLatestCompactionEntry (core/session-manager.ts:372), parseSkillBlock (core/agent-session.ts:177) and
formatSkillsForPrompt (core/skills.ts:358)."""
from dataclasses import dataclass
from typing import Any, Mapping, Optional, Sequence

CURRENT_SESSION_VERSION = 3
"""Pi's CURRENT_SESSION_VERSION: the version a session file's header carries."""
VIRTUAL_MODEL_STATE_ENTRY = "pi.virtual-model-state"
"""Pi's VIRTUAL_MODEL_STATE_ENTRY: the custom entry type that persists the virtual model state."""


def get_latest_compaction_entry(entries: Sequence[Mapping[str, Any]]) -> Optional[Mapping[str, Any]]:
    """Pi's getLatestCompactionEntry: the last "compaction" entry of the session entries, or None."""
    for entry in reversed(entries):
        if isinstance(entry, Mapping) and entry.get("type") == "compaction":
            return entry
    return None


@dataclass(frozen=True)
class ParsedSkillBlock:
    """Pi's ParsedSkillBlock, with Python names."""

    name: str
    location: str
    content: str
    user_message: Optional[str]
    """The trimmed text after the block, None where Pi's is undefined (no text, or only white space)."""


_JS_WHITESPACE = "\t\n\v\f\r \u00a0\u1680\u2028\u2029\u202f\u205f\u3000\ufeff" + "".join(chr(c) for c in range(0x2000, 0x200B))


def _js_trim(text: str) -> str:
    """JavaScript's String.prototype.trim: the ECMAScript white space and line terminators, which include U+FEFF and not U+0085."""
    return text.strip(_JS_WHITESPACE)


def parse_skill_block(text: str) -> Optional[ParsedSkillBlock]:
    """Pi's parseSkillBlock: it reads ``<skill name="N" location="L">\\n CONTENT \\n</skill>`` optionally followed by ``\\n\\n`` and the user's message, where the
    block must span the whole text, or returns None. The content ends at the first ``\\n</skill>`` that leaves either nothing or ``\\n\\n`` and at least one more
    character, as Pi's lazy pattern does."""
    prefix = '<skill name="'
    if not text.startswith(prefix):
        return None
    name, quote, rest = text[len(prefix):].partition('"')
    if not quote or not name or not rest.startswith(' location="'):
        return None
    location, quote, rest = rest[len(' location="'):].partition('"')
    if not quote or not location or not rest.startswith(">\n"):
        return None
    rest = rest[2:]
    closing = "\n</skill>"
    offset = 0
    while True:
        at = rest.find(closing, offset)
        if at < 0:
            return None
        after = rest[at + len(closing):]
        if after == "":
            return ParsedSkillBlock(name, location, rest[:at], None)
        if after.startswith("\n\n") and len(after) > 2:
            return ParsedSkillBlock(name, location, rest[:at], _js_trim(after[2:]) or None)
        offset = at + 1


def _js_truthy(value: Any) -> bool:
    if value is None:
        return False
    if isinstance(value, (bool, str, int, float)):
        return bool(value) and not (isinstance(value, float) and value != value)
    return True


def _escape_xml(text: Any) -> str:
    return str(text if isinstance(text, str) else "").replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;").replace('"', "&quot;").replace("'", "&apos;")


def format_skills_for_prompt(skills: Sequence[Mapping[str, Any]], file_read_tool: str = "read") -> str:
    """Pi's formatSkillsForPrompt over skill dicts (the ``skills`` of the system prompt options): the XML block that lists the skills a model may load,
    without those that disable model invocation. ``file_read_tool`` names the tool that loads a skill's file: "read" (Pi's default), "bash", or any other
    value for a reader reachable only through another tool ("indirect")."""
    visible = [skill for skill in skills if not _js_truthy(skill.get("disableModelInvocation"))]
    if not visible:
        return ""
    if file_read_tool == "read":
        load = "Use the read tool to load a skill's file when the task matches its description."
    elif file_read_tool == "bash":
        load = "Use bash to load a skill's file when the task matches its description."
    else:
        load = "Load a skill's file when the task matches its description."
    lines = [
        "\n\nThe following skills provide specialized instructions for specific tasks.",
        load,
        "When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
        "",
        "<available_skills>",
    ]
    for skill in visible:
        lines.append("  <skill>")
        lines.append("    <name>%s</name>" % _escape_xml(skill.get("name")))
        lines.append("    <description>%s</description>" % _escape_xml(skill.get("description")))
        lines.append("    <location>%s</location>" % _escape_xml(skill.get("filePath")))
        lines.append("  </skill>")
    lines.append("</available_skills>")
    return "\n".join(lines)
