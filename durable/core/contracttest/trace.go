package contracttest

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Param is one bound statement parameter: nil, int64, float64, string or []byte.
type Param any

// Stmt is one statement of a commit with its bound parameters.
type Stmt struct {
	SQL    string
	Params []Param
}

// Writer writes the trace format of CONTRACT section 7.6 (JSON Lines) for a native run. It records each commit's statements
// only; durable/contract/lib/replay.mjs derives the row changes by replaying them onto the store the run started from.
// Lines are written when called, so a process killed after a commit has already written every line up to it.
type Writer struct {
	mu    sync.Mutex
	f     *os.File
	texts map[string]int
	crash int64
	has   bool
}

// NewWriter creates the trace file. The crash switch is read from CONTRACT_CRASH_AFTER, the variable the Node tools set.
func NewWriter(path string) (*Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := &Writer{f: f, texts: map[string]int{}}
	if v := os.Getenv("CONTRACT_CRASH_AFTER"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("CONTRACT_CRASH_AFTER: %w", err)
		}
		w.crash, w.has = n, true
	}
	return w, nil
}

func (w *Writer) line(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.f.Write(append(b, '\n'))
	return err
}

// Meta writes the first line.
func (w *Writer) Meta(scenario, impl string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.line(map[string]any{"t": "meta", "scenario": scenario, "impl": impl})
}

// Store names the store a run opened.
func (w *Writer) Store(label, path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.line(map[string]any{"t": "store", "store": label, "path": path})
}

// Commit records one committed transaction. Call it after the commit succeeded in the store; it then honours the crash switch.
func (w *Writer) Commit(store string, seq int64, stmts []Stmt) error {
	w.mu.Lock()
	err := w.commit(store, seq, stmts)
	w.mu.Unlock()
	if err == nil && w.has && seq >= w.crash {
		// A crash is abandonment: no close, no flush, no deferred function (CONTRACT 5.5).
		if p, perr := os.FindProcess(os.Getpid()); perr == nil {
			_ = p.Kill()
		}
		os.Exit(137)
	}
	return err
}

func (w *Writer) commit(store string, seq int64, stmts []Stmt) error {
	out := make([]map[string]any, 0, len(stmts))
	for _, s := range stmts {
		q, ok := w.texts[s.SQL]
		if !ok {
			q = len(w.texts)
			w.texts[s.SQL] = q
			if err := w.line(map[string]any{"t": "sql", "store": store, "q": q, "text": s.SQL}); err != nil {
				return err
			}
		}
		ps := make([]any, len(s.Params))
		for i, p := range s.Params {
			switch v := p.(type) {
			case []byte:
				ps[i] = map[string]string{"blob": hex.EncodeToString(v)}
			case int:
				ps[i] = int64(v)
			default:
				ps[i] = v
			}
		}
		out = append(out, map[string]any{"q": q, "k": "w", "p": ps})
	}
	return w.line(map[string]any{"t": "commit", "store": store, "seq": seq, "stmts": out})
}

// Model records a model call's bench fingerprint (a native core that does not build the full pi-ai Context writes only this).
func (w *Writer) Model(call int, bench string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.line(map[string]any{"t": "model", "call": call, "bench": bench})
}

// Out records one line of the scenario's output.
func (w *Writer) Out(text string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.line(map[string]any{"t": "out", "stream": "stdout", "text": text})
}

// Close writes the end line and closes the file.
func (w *Writer) Close(code int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.line(map[string]any{"t": "end", "code": code, "signal": nil}); err != nil {
		return err
	}
	return w.f.Close()
}

// Line is one trace line, kept as the raw fields so unknown line types survive.
type Line struct {
	T      string
	Fields map[string]json.RawMessage
}

// ReadTrace reads a trace file.
func ReadTrace(path string) ([]Line, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Line
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var f map[string]json.RawMessage
		if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var t string
		_ = json.Unmarshal(f["t"], &t)
		out = append(out, Line{T: t, Fields: f})
	}
	return out, sc.Err()
}

// DecodeParams reads the `p` array of a statement from a trace or a script: integers are int64, other numbers float64, a
// {"blob": hex} object is []byte. A host that binds parameters must keep the integer/real distinction (SQLite STRICT tables).
func DecodeParams(raw json.RawMessage) ([]Param, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var in []any
	if err := dec.Decode(&in); err != nil {
		return nil, err
	}
	out := make([]Param, len(in))
	for i, v := range in {
		switch x := v.(type) {
		case json.Number:
			if strings.ContainsAny(string(x), ".eE") {
				f, err := x.Float64()
				if err != nil {
					return nil, err
				}
				out[i] = f
			} else {
				n, err := x.Int64()
				if err != nil {
					return nil, err
				}
				out[i] = n
			}
		case map[string]any:
			h, ok := x["blob"].(string)
			if !ok {
				return nil, fmt.Errorf("parameter %d: object without blob", i)
			}
			b, err := hex.DecodeString(h)
			if err != nil {
				return nil, err
			}
			out[i] = b
		default:
			out[i] = v
		}
	}
	return out, nil
}
