// SPDX-License-Identifier: MIT
//! Replays the corpus in <corpus dir>/plan.txt through alacritty_terminal and prints, as JSON lines, the engine and the screen after
//! every step (docs/testing/term-engines.md).

use std::{fs, path::Path};

use alacritty_terminal::{
    event::{Event, EventListener},
    grid::Dimensions,
    index::{Column, Line, Point},
    term::{cell::Flags, Config, Term, TermMode},
    vte::ansi::{Color, Processor},
};
use serde_json::{json, Value};

struct Size {
    cols: usize,
    rows: usize,
}

impl Dimensions for Size {
    fn total_lines(&self) -> usize {
        self.rows
    }
    fn screen_lines(&self) -> usize {
        self.rows
    }
    fn columns(&self) -> usize {
        self.cols
    }
}

struct Quiet;

impl EventListener for Quiet {
    fn send_event(&self, _: Event) {}
}

struct Step {
    end: usize,
    resize: Option<(usize, usize)>,
}

struct Variant {
    name: String,
    file: String,
    steps: Vec<Step>,
}

struct Scenario {
    name: String,
    cols: usize,
    rows: usize,
    variants: Vec<Variant>,
}

fn read_plan(dir: &Path) -> Vec<Scenario> {
    let text = fs::read_to_string(dir.join("plan.txt")).expect("plan.txt");
    let mut scenarios: Vec<Scenario> = Vec::new();
    for line in text.lines().filter(|line| !line.is_empty()) {
        let fields: Vec<&str> = line.split('\t').collect();
        match fields[0] {
            "scenario" => scenarios.push(Scenario {
                name: fields[1].to_string(),
                cols: fields[2].parse().unwrap(),
                rows: fields[3].parse().unwrap(),
                variants: Vec::new(),
            }),
            "variant" => scenarios.last_mut().unwrap().variants.push(Variant {
                name: fields[1].to_string(),
                file: fields[2].to_string(),
                steps: Vec::new(),
            }),
            _ => {
                let resize = (fields.len() > 2).then(|| (fields[2].parse().unwrap(), fields[3].parse().unwrap()));
                let variant = scenarios.last_mut().unwrap().variants.last_mut().unwrap();
                variant.steps.push(Step { end: fields[1].parse().unwrap(), resize });
            }
        }
    }
    scenarios
}

fn color(prefix: &str, color: Color) -> Option<String> {
    match color {
        // The default foreground and background are what an unstyled cell has, so they are no style.
        Color::Named(name) if name as usize >= 256 => None,
        Color::Named(name) => Some(format!("{prefix}{}", name as usize)),
        Color::Indexed(index) => Some(format!("{prefix}{index}")),
        Color::Spec(rgb) => Some(format!("{prefix}#{:02x}{:02x}{:02x}", rgb.r, rgb.g, rgb.b)),
    }
}

/// Whether the scroll region is the whole screen after the first `steps` of the variant. alacritty_terminal keeps the region private, so it is
/// probed on a second terminal that replays the same bytes: a line feed on the last row scrolls only when the region ends there, and a reverse
/// index on the first row only when it starts there. The probe changes the second terminal, never the one that is dumped.
fn margins_full(scenario: &Scenario, data: &[u8], steps: &[Step]) -> bool {
    let (mut cols, mut rows) = (scenario.cols, scenario.rows);
    let mut term = Term::new(Config::default(), &Size { cols, rows }, Quiet);
    let mut parser: Processor = Processor::new();
    let mut start = 0;
    for step in steps {
        if let Some((c, r)) = step.resize {
            (cols, rows) = (c, r);
            term.resize(Size { cols, rows });
        }
        parser.advance(&mut term, &data[start..step.end]);
        start = step.end;
    }
    // Origin mode off, so the cursor addresses the screen; every row marked with a letter.
    let mut probe = String::from("\x1b[?6l");
    for row in 0..rows {
        probe.push_str(&format!("\x1b[{};1H{}", row + 1, (b'A' + (row % 26) as u8) as char));
    }
    probe.push_str(&format!("\x1b[{rows};1H\n"));
    parser.advance(&mut term, probe.as_bytes());
    let bottom_is_last_row = term.grid()[Point::new(Line(rows as i32 - 1), Column(0))].c == ' ';
    parser.advance(&mut term, b"\x1b[1;1H\x1bM");
    let top_is_first_row = term.grid()[Point::new(Line(0), Column(0))].c == ' ';
    bottom_is_last_row && top_is_first_row
}

fn dump(term: &Term<Quiet>, cols: usize, rows: usize, full_margins: bool) -> Value {
    let grid = term.grid();
    let mut texts = Vec::new();
    let mut styles = Vec::new();
    for y in 0..rows {
        let mut text = String::new();
        let mut runs: Vec<(usize, usize, String)> = Vec::new();
        for x in 0..cols {
            let cell = &grid[Point::new(Line(y as i32), Column(x))];
            if cell.flags.contains(Flags::WIDE_CHAR_SPACER) {
                continue;
            }
            text.push(cell.c);
            if let Some(extra) = cell.zerowidth() {
                text.extend(extra.iter());
            }
            let width = if cell.flags.contains(Flags::WIDE_CHAR) { 2 } else { 1 };
            let mut parts: Vec<String> = Vec::new();
            parts.extend(color("fg", cell.fg));
            parts.extend(color("bg", cell.bg));
            for (flag, name) in [
                (Flags::BOLD, "bold"),
                (Flags::ITALIC, "italic"),
                (Flags::DIM, "dim"),
                (Flags::UNDERLINE, "underline"),
                (Flags::DOUBLE_UNDERLINE, "double-underline"),
                (Flags::UNDERCURL, "undercurl"),
                (Flags::DOTTED_UNDERLINE, "dotted-underline"),
                (Flags::DASHED_UNDERLINE, "dashed-underline"),
                (Flags::INVERSE, "inverse"),
                (Flags::HIDDEN, "invisible"),
                (Flags::STRIKEOUT, "strike"),
            ] {
                if cell.flags.contains(flag) {
                    parts.push(name.to_string());
                }
            }
            let style = parts.join(" ");
            match runs.last_mut() {
                Some(last) if last.2 == style && last.0 + last.1 == x => last.1 += width,
                _ if !style.is_empty() => runs.push((x, width, style)),
                _ => {}
            }
        }
        texts.push(text);
        styles.push(runs.into_iter().map(|(x, n, s)| json!([x, n, s])).collect::<Vec<_>>());
    }
    let cursor = grid.cursor.point;
    let margins = json!({ "top": null, "bottom": null, "origin": term.mode().contains(TermMode::ORIGIN), "full": full_margins });
    json!({ "cursor": [cursor.column.0, cursor.line.0], "rows": texts, "styles": styles, "margins": margins })
}

fn main() {
    let dir = std::env::args().nth(1).expect("corpus directory");
    let dir = Path::new(&dir);
    println!("{}", json!({ "engine": "alacritty_terminal" }));
    for scenario in read_plan(dir) {
        for variant in &scenario.variants {
            let data = fs::read(dir.join(&variant.file)).expect("variant file");
            let (mut cols, mut rows) = (scenario.cols, scenario.rows);
            let mut term = Term::new(Config::default(), &Size { cols, rows }, Quiet);
            let mut parser: Processor = Processor::new();
            let mut checkpoints = Vec::new();
            let mut start = 0;
            for (index, step) in variant.steps.iter().enumerate() {
                if let Some((c, r)) = step.resize {
                    (cols, rows) = (c, r);
                    term.resize(Size { cols, rows });
                }
                parser.advance(&mut term, &data[start..step.end]);
                start = step.end;
                checkpoints.push(dump(&term, cols, rows, margins_full(&scenario, &data, &variant.steps[..=index])));
            }
            println!("{}", json!({ "scenario": scenario.name, "variant": variant.name, "checkpoints": checkpoints }));
        }
    }
}
