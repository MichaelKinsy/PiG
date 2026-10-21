"""Pi's coding-agent theme helpers (``modes/interactive/theme/theme.ts``): the component themes of pi-tui built from the process-global theme,
``highlightCode`` and ``initTheme``. Pi's theme is global, so these read the global theme each time a function runs: the theme the host last published, or the
one :func:`init_theme` installed since. The host has the highlight.js port, so :func:`highlight_code` asks it (the ``highlight.code`` host call) and a failed
call takes the same lines Pi returns for a failed highlight."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Callable, Optional

from . import _tui_state, host_helpers

StyleFn = Callable[[str], str]
SelectedStyleFn = Callable[[str, bool], str]


@dataclass(frozen=True)
class MarkdownTheme:
    """pi-tui's ``MarkdownTheme`` as ``getMarkdownTheme`` returns it: every style is a function of the global theme. ``highlight_code`` is
    ``getMarkdownTheme().highlightCode``: :func:`highlight_code`, with the ``mdCodeBlock`` color for a highlight that fails."""

    heading: StyleFn
    link: StyleFn
    link_url: StyleFn
    code: StyleFn
    code_block: StyleFn
    code_block_border: StyleFn
    quote: StyleFn
    quote_border: StyleFn
    hr: StyleFn
    list_bullet: StyleFn
    bold: StyleFn
    italic: StyleFn
    underline: StyleFn
    strikethrough: StyleFn
    highlight_code: Callable[[str, Optional[str]], "list[str]"]


@dataclass(frozen=True)
class SelectListTheme:
    """pi-tui's ``SelectListTheme`` as ``getSelectListTheme`` returns it."""

    selected_prefix: StyleFn
    selected_text: StyleFn
    description: StyleFn
    scroll_info: StyleFn
    no_match: StyleFn


@dataclass(frozen=True)
class SettingsListTheme:
    """pi-tui's ``SettingsListTheme`` as ``getSettingsListTheme`` returns it. ``cursor`` is drawn when the theme is built."""

    label: SelectedStyleFn
    value: SelectedStyleFn
    description: StyleFn
    cursor: str
    hint: StyleFn


@dataclass(frozen=True)
class EditorTheme:
    """pi-tui's ``EditorTheme`` as ``getEditorTheme`` returns it."""

    border_color: StyleFn
    select_list: SelectListTheme


def _foreground_of(token: str) -> StyleFn:
    return lambda text: _tui_state.current_host_theme().fg(token, text)


def get_markdown_theme() -> MarkdownTheme:
    """pi-coding-agent's ``getMarkdownTheme``."""
    return MarkdownTheme(
        heading=_foreground_of("mdHeading"),
        link=_foreground_of("mdLink"),
        link_url=_foreground_of("mdLinkUrl"),
        code=_foreground_of("mdCode"),
        code_block=_foreground_of("mdCodeBlock"),
        code_block_border=_foreground_of("mdCodeBlockBorder"),
        quote=_foreground_of("mdQuote"),
        quote_border=_foreground_of("mdQuoteBorder"),
        hr=_foreground_of("mdHr"),
        list_bullet=_foreground_of("mdListBullet"),
        bold=lambda text: _tui_state.current_host_theme().bold(text),
        italic=lambda text: _tui_state.current_host_theme().italic(text),
        underline=lambda text: _tui_state.current_host_theme().underline(text),
        strikethrough=lambda text: _tui_state.current_host_theme().strikethrough(text),
        highlight_code=lambda code, lang=None: _highlight(code, lang, True),
    )


def get_select_list_theme() -> SelectListTheme:
    """pi-coding-agent's ``getSelectListTheme``."""
    return SelectListTheme(
        selected_prefix=_foreground_of("accent"),
        selected_text=_foreground_of("accent"),
        description=_foreground_of("muted"),
        scroll_info=_foreground_of("muted"),
        no_match=_foreground_of("muted"),
    )


def get_editor_theme() -> EditorTheme:
    """``theme.ts`` ``getEditorTheme``, which the interactive mode builds its editor with."""
    return EditorTheme(border_color=_foreground_of("borderMuted"), select_list=get_select_list_theme())


def get_settings_list_theme() -> SettingsListTheme:
    """pi-coding-agent's ``getSettingsListTheme``."""
    accent, muted, dim = _foreground_of("accent"), _foreground_of("muted"), _foreground_of("dim")
    return SettingsListTheme(
        label=lambda text, selected: accent(text) if selected else text,
        value=lambda text, selected: accent(text) if selected else muted(text),
        description=dim,
        cursor=accent("→ "),
        hint=dim,
    )


def highlight_code(code: str, lang: Optional[str] = None) -> list[str]:
    """pi-coding-agent's ``highlightCode``: ``code`` highlighted for ``lang`` with the global theme, one string per line. Code in a language the host's
    highlight.js does not support, or with no language, takes the ``mdCodeBlock`` color; code highlight.js fails on stays unstyled. The host's highlighter
    runs it; a host that cannot be reached leaves the code unstyled, as a failed highlight does."""
    return _highlight(code, lang, False)


def _highlight(code: str, lang: Optional[str], markdown: bool) -> list[str]:
    if not lang:
        # No language: Pi highlights nothing, whatever the host would answer.
        return _code_block_lines(code)
    try:
        return host_helpers.highlight_code_lines(code, lang, markdown, _tui_state.installed_theme_palette())
    except Exception:  # noqa: BLE001 - any failure of the host call is a failed highlight, as in Pi's catch
        return _code_block_lines(code) if markdown else code.split("\n")


def _code_block_lines(code: str) -> list[str]:
    theme = _tui_state.current_host_theme()
    return [theme.fg("mdCodeBlock", line) for line in code.split("\n")]


def init_theme(name: Optional[str] = None) -> None:
    """pi-coding-agent's ``initTheme``: it makes the host's theme of that name, or the system theme for None, an empty name or one the host has no theme of,
    the global theme, which every helper above reads, until the host's own theme changes. The host answers over the ``theme.load`` call; an error means it could
    not be reached and the global theme is unchanged. Pi's ``enableWatcher`` has no counterpart: the host owns its theme files."""
    _tui_state.install_theme(host_helpers.load_theme(name or None))
