// SPDX-License-Identifier: MIT
//! Replays the corpus in <corpus dir>/plan.txt through wezterm-term and prints, as JSON lines, the engine and the screen after every
//! step (docs/testing/term-engines.md).

use std::{fs, path::Path, sync::Arc};

use serde_json::{json, Value};
use wezterm_term::{
    color::{ColorAttribute, ColorPalette},
    Intensity, Terminal, TerminalConfiguration, TerminalSize, Underline,
};

#[derive(Debug)]
struct Config;

impl TerminalConfiguration for Config {
    fn scrollback_size(&self) -> usize {
        0
    }
    fn color_palette(&self) -> ColorPalette {
        ColorPalette::default()
    }
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
fn size(cols: usize, rows: usize) -> TerminalSize {
    TerminalSize { rows, cols, pixel_width: 0, pixel_height: 0, dpi: 0 }
}

fn dump(term: &Terminal, cols: usize, rows: usize) -> Value {
    let screen = term.screen();
    let top = screen.phys_row(0);
    let lines = screen.lines_in_phys_range(top..top + rows);
    let mut texts = Vec::new();
    let mut styles = Vec::new();
    for line in &lines {
        let mut text = String::new();
        let mut runs: Vec<(usize, usize, String)> = Vec::new();
        let mut next = 0;
        for cell in line.visible_cells() {
            let x = cell.cell_index();
            if x >= cols {
                break;
            }
            while next < x {
                text.push(' ');
                next += 1;
            }
            text.push_str(cell.str());
            let width = cell.width();
            next = x + width;
            let attrs = cell.attrs();
            let mut parts: Vec<String> = Vec::new();
            if attrs.foreground() != ColorAttribute::Default {
                parts.push(format!("fg{:?}", attrs.foreground()));
            }
            if attrs.background() != ColorAttribute::Default {
                parts.push(format!("bg{:?}", attrs.background()));
            }
            match attrs.intensity() {
                Intensity::Normal => {}
                Intensity::Bold => parts.push("bold".to_string()),
                Intensity::Half => parts.push("dim".to_string()),
            }
            if attrs.italic() {
                parts.push("italic".to_string());
            }
            if attrs.underline() != Underline::None {
                parts.push(format!("underline{:?}", attrs.underline()));
            }
            for (flag, name) in [
                (attrs.strikethrough(), "strike"),
                (attrs.reverse(), "inverse"),
                (attrs.invisible(), "invisible"),
                (attrs.overline(), "overline"),
                (attrs.hyperlink().is_some(), "hyperlink"),
            ] {
                if flag {
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
        while next < cols {
            text.push(' ');
            next += 1;
        }
        texts.push(text);
        styles.push(runs.into_iter().map(|(x, n, s)| json!([x, n, s])).collect::<Vec<_>>());
    }
    let cursor = term.cursor_pos();
    let margins = term.get_top_and_bottom_margins();
    let (top, bottom) = (margins.start, margins.end - 1);
    let margins = json!({ "top": top, "bottom": bottom, "origin": term.dec_origin_mode_enabled(), "full": top == 0 && bottom == rows as i64 - 1 });
    json!({ "cursor": [cursor.x, cursor.y], "rows": texts, "styles": styles, "margins": margins })
}

fn main() {
    let dir = std::env::args().nth(1).expect("corpus directory");
    let dir = Path::new(&dir);
    println!("{}", json!({ "engine": "wezterm-term" }));
    for scenario in read_plan(dir) {
        for variant in &scenario.variants {
            let data = fs::read(dir.join(&variant.file)).expect("variant file");
            let (mut cols, mut rows) = (scenario.cols, scenario.rows);
            let mut term = Terminal::new(size(cols, rows), Arc::new(Config), "pig", "0", Box::new(std::io::sink()));
            let mut checkpoints = Vec::new();
            let mut start = 0;
            for step in &variant.steps {
                if let Some((c, r)) = step.resize {
                    (cols, rows) = (c, r);
                    term.resize(size(cols, rows));
                }
                term.advance_bytes(&data[start..step.end]);
                start = step.end;
                checkpoints.push(dump(&term, cols, rows));
            }
            println!("{}", json!({ "scenario": scenario.name, "variant": variant.name, "checkpoints": checkpoints }));
        }
    }
}
