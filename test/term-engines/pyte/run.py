# SPDX-License-Identifier: MIT
"""Replays the corpus in <corpus dir>/plan.txt through pyte and prints, as JSON lines, the engine and the screen after every step."""
import json
import sys
from importlib.metadata import version

import pyte


def style(char):
    parts = []
    if char.fg != "default":
        parts.append("fg" + char.fg)
    if char.bg != "default":
        parts.append("bg" + char.bg)
    for flag, name in ((char.bold, "bold"), (char.italics, "italic"), (char.underscore, "underline"), (char.strikethrough, "strike"),
                       (char.reverse, "inverse"), (char.blink, "blink")):
        if flag:
            parts.append(name)
    return " ".join(parts)


def dump(screen):
    rows, styles = [], []
    for y in range(screen.lines):
        line = screen.buffer[y]
        text, runs = "", []
        for x in range(screen.columns):
            char = line[x]
            text += char.data or ""
            s = style(char)
            if runs and runs[-1][2] == s and runs[-1][0] + runs[-1][1] == x:
                runs[-1][1] += 1
            elif s:
                runs.append([x, 1, s])
        rows.append(text)
        styles.append(runs)
    return {"cursor": [screen.cursor.x, screen.cursor.y], "rows": rows, "styles": styles}


def main():
    directory = sys.argv[1]
    scenarios = []
    with open(f"{directory}/plan.txt", encoding="utf8") as plan:
        for line in plan.read().splitlines():
            fields = line.split("\t")
            if fields[0] == "scenario":
                scenarios.append({"name": fields[1], "cols": int(fields[2]), "rows": int(fields[3]), "variants": []})
            elif fields[0] == "variant":
                scenarios[-1]["variants"].append({"name": fields[1], "file": fields[2], "steps": []})
            else:
                resize = [int(fields[2]), int(fields[3])] if len(fields) > 2 else None
                scenarios[-1]["variants"][-1]["steps"].append((int(fields[1]), resize))
    print(json.dumps({"engine": "pyte", "version": version("pyte")}))
    for scenario in scenarios:
        for variant in scenario["variants"]:
            with open(f"{directory}/{variant['file']}", "rb") as data_file:
                data = data_file.read()
            screen = pyte.Screen(scenario["cols"], scenario["rows"])
            stream = pyte.ByteStream(screen)
            checkpoints, start = [], 0
            for end, resize in variant["steps"]:
                if resize:
                    screen.resize(lines=resize[1], columns=resize[0])
                stream.feed(data[start:end])
                start = end
                checkpoints.append(dump(screen))
            print(json.dumps({"scenario": scenario["name"], "variant": variant["name"], "checkpoints": checkpoints}, ensure_ascii=False))


main()
