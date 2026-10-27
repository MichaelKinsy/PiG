// SPDX-License-Identifier: MIT
// Replays the corpus in <corpus dir>/plan.txt through libvterm (the terminal of Neovim and Vim) and prints, as JSON lines, the engine and
// the screen after every step (docs/testing/term-engines.md).
#define _POSIX_C_SOURCE 200809L
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "vterm.h"

static void put_json_string(FILE *out, const char *s, size_t n) {
  fputc('"', out);
  for (size_t i = 0; i < n; i++) {
    unsigned char c = (unsigned char)s[i];
    if (c == '"' || c == '\\') fprintf(out, "\\%c", c);
    else if (c < 0x20) fprintf(out, "\\u%04x", c);
    else fputc(c, out);
  }
  fputc('"', out);
}

static size_t utf8_encode(uint32_t cp, char *buf) {
  if (cp < 0x80) { buf[0] = (char)cp; return 1; }
  if (cp < 0x800) { buf[0] = (char)(0xC0 | cp >> 6); buf[1] = (char)(0x80 | (cp & 0x3F)); return 2; }
  if (cp < 0x10000) { buf[0] = (char)(0xE0 | cp >> 12); buf[1] = (char)(0x80 | (cp >> 6 & 0x3F)); buf[2] = (char)(0x80 | (cp & 0x3F)); return 3; }
  buf[0] = (char)(0xF0 | cp >> 18); buf[1] = (char)(0x80 | (cp >> 12 & 0x3F)); buf[2] = (char)(0x80 | (cp >> 6 & 0x3F)); buf[3] = (char)(0x80 | (cp & 0x3F));
  return 4;
}

static void color_string(char *out, size_t n, const char *prefix, const VTermColor *c, int is_default) {
  out[0] = 0;
  if (is_default) return;
  if (VTERM_COLOR_IS_INDEXED(c)) snprintf(out, n, "%s%d", prefix, c->indexed.idx);
  else snprintf(out, n, "%s#%02x%02x%02x", prefix, c->rgb.red, c->rgb.green, c->rgb.blue);
}

static void add_word(char *out, size_t n, const char *word) {
  size_t len = strlen(out);
  snprintf(out + len, n - len, "%s%s", len ? " " : "", word);
}

static void cell_style(char *out, size_t n, const VTermScreenCell *cell) {
  char word[32];
  out[0] = 0;
  color_string(word, sizeof word, "fg", &cell->fg, VTERM_COLOR_IS_DEFAULT_FG(&cell->fg));
  if (*word) add_word(out, n, word);
  color_string(word, sizeof word, "bg", &cell->bg, VTERM_COLOR_IS_DEFAULT_BG(&cell->bg));
  if (*word) add_word(out, n, word);
  if (cell->attrs.bold) add_word(out, n, "bold");
  if (cell->attrs.italic) add_word(out, n, "italic");
  if (cell->attrs.underline == VTERM_UNDERLINE_SINGLE) add_word(out, n, "underline");
  if (cell->attrs.underline == VTERM_UNDERLINE_DOUBLE) add_word(out, n, "double-underline");
  if (cell->attrs.underline == VTERM_UNDERLINE_CURLY) add_word(out, n, "curly-underline");
  if (cell->attrs.blink) add_word(out, n, "blink");
  if (cell->attrs.reverse) add_word(out, n, "inverse");
  if (cell->attrs.conceal) add_word(out, n, "invisible");
  if (cell->attrs.strike) add_word(out, n, "strike");
  if (cell->attrs.small) add_word(out, n, "small");
  if (cell->attrs.baseline == VTERM_BASELINE_RAISE) add_word(out, n, "raise");
  if (cell->attrs.baseline == VTERM_BASELINE_LOWER) add_word(out, n, "lower");
}

static void dump(FILE *out, VTerm *vt, VTermScreen *screen, int cols, int rows) {
  fputs("{\"rows\":[", out);
  VTermScreenCell cell;
  // First pass: the text of every row; second pass: the style runs.
  for (int y = 0; y < rows; y++) {
    fputs(y ? "," : "", out);
    char *text = malloc((size_t)cols * 4 * VTERM_MAX_CHARS_PER_CELL + 1);
    size_t len = 0;
    for (int x = 0; x < cols; x++) {
      VTermPos pos = {y, x};
      vterm_screen_get_cell(screen, pos, &cell);
      if (cell.chars[0] == (uint32_t)-1) continue;
      if (cell.chars[0] == 0) { text[len++] = ' '; continue; }
      for (int i = 0; i < VTERM_MAX_CHARS_PER_CELL && cell.chars[i]; i++) len += utf8_encode(cell.chars[i], text + len);
    }
    put_json_string(out, text, len);
    free(text);
  }
  fputs("],\"styles\":[", out);
  for (int y = 0; y < rows; y++) {
    fputs(y ? ",[" : "[", out);
    char run_style[128] = "";
    int run_x = 0, run_len = 0, first = 1;
    for (int x = 0; x < cols; x++) {
      VTermPos pos = {y, x};
      vterm_screen_get_cell(screen, pos, &cell);
      if (cell.chars[0] == (uint32_t)-1) continue;
      char style[128];
      cell_style(style, sizeof style, &cell);
      int width = cell.width > 0 ? cell.width : 1;
      if (run_len && strcmp(style, run_style) == 0 && run_x + run_len == x) { run_len += width; continue; }
      if (run_len && *run_style) {
        fprintf(out, "%s[%d,%d,", first ? "" : ",", run_x, run_len);
        put_json_string(out, run_style, strlen(run_style));
        fputc(']', out);
        first = 0;
      }
      strcpy(run_style, style);
      run_x = x;
      run_len = width;
    }
    if (run_len && *run_style) {
      fprintf(out, "%s[%d,%d,", first ? "" : ",", run_x, run_len);
      put_json_string(out, run_style, strlen(run_style));
      fputc(']', out);
    }
    fputc(']', out);
  }
  VTermPos cursor;
  vterm_state_get_cursorpos(vterm_obtain_state(vt), &cursor);
  fprintf(out, "],\"cursor\":[%d,%d]}", cursor.col, cursor.row);
}

static unsigned char *read_file(const char *path, size_t *size) {
  FILE *f = fopen(path, "rb");
  if (!f) { fprintf(stderr, "cannot open %s\n", path); exit(1); }
  fseek(f, 0, SEEK_END);
  long n = ftell(f);
  fseek(f, 0, SEEK_SET);
  unsigned char *data = malloc((size_t)n + 1);
  if (fread(data, 1, (size_t)n, f) != (size_t)n) { fprintf(stderr, "cannot read %s\n", path); exit(1); }
  fclose(f);
  *size = (size_t)n;
  return data;
}

typedef struct {
  FILE *line;  // the JSON line being built, in memory
  char *buf;
  size_t buf_len;
  VTerm *vt;
  VTermScreen *screen;
  unsigned char *data;
  size_t data_len, start;
  int cols, rows, steps;
  int scenario_cols, scenario_rows;
} Replay;

static void finish_variant(Replay *r) {
  if (!r->vt) return;
  fputs("]}\n", r->line);
  fclose(r->line);
  fwrite(r->buf, 1, r->buf_len, stdout);
  free(r->buf);
  vterm_free(r->vt);
  free(r->data);
  r->vt = NULL;
}

int main(int argc, char **argv) {
  if (argc != 2) { fprintf(stderr, "usage: run <corpus dir>\n"); return 2; }
  char path[4096];
  snprintf(path, sizeof path, "%s/plan.txt", argv[1]);
  FILE *plan = fopen(path, "r");
  if (!plan) { fprintf(stderr, "cannot open %s\n", path); return 1; }
  puts("{\"engine\":\"libvterm\"}");
  Replay r = {0};
  char scenario[256] = "";
  char *line = NULL;
  size_t cap = 0;
  while (getline(&line, &cap, plan) > 0) {
    line[strcspn(line, "\n")] = 0;
    if (!*line) continue;
    char *fields[5] = {0};
    int count = 0;
    for (char *p = line; count < 5; ) { fields[count++] = p; char *tab = strchr(p, '\t'); if (!tab) break; *tab = 0; p = tab + 1; }
    if (strcmp(fields[0], "scenario") == 0) {
      finish_variant(&r);
      snprintf(scenario, sizeof scenario, "%s", fields[1]);
      r.scenario_cols = atoi(fields[2]);
      r.scenario_rows = atoi(fields[3]);
    } else if (strcmp(fields[0], "variant") == 0) {
      finish_variant(&r);
      snprintf(path, sizeof path, "%s/%s", argv[1], fields[2]);
      r.data = read_file(path, &r.data_len);
      r.start = 0;
      r.steps = 0;
      r.cols = r.scenario_cols;
      r.rows = r.scenario_rows;
      r.vt = vterm_new(r.rows, r.cols);
      vterm_set_utf8(r.vt, 1);
      r.screen = vterm_obtain_screen(r.vt);
      vterm_screen_enable_altscreen(r.screen, 1);
      vterm_screen_reset(r.screen, 1);
      r.line = open_memstream(&r.buf, &r.buf_len);
      fputs("{\"scenario\":", r.line);
      put_json_string(r.line, scenario, strlen(scenario));
      fprintf(r.line, ",\"variant\":\"%s\",\"checkpoints\":[", fields[1]);
    } else {
      size_t end = (size_t)atol(fields[1]);
      if (count > 2) {
        r.cols = atoi(fields[2]);
        r.rows = atoi(fields[3]);
        vterm_set_size(r.vt, r.rows, r.cols);
      }
      vterm_input_write(r.vt, (const char *)r.data + r.start, end - r.start);
      vterm_screen_flush_damage(r.screen);
      r.start = end;
      if (r.steps++) fputc(',', r.line);
      dump(r.line, r.vt, r.screen, r.cols, r.rows);
    }
  }
  finish_variant(&r);
  free(line);
  fclose(plan);
  return 0;
}
