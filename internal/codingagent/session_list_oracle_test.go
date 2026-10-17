package codingagent

import (
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type listedSession struct {
	File            string   `json:"file"`
	ID              string   `json:"id"`
	CWD             string   `json:"cwd"`
	Name            string   `json:"name"`
	Parent          string   `json:"parent"`
	Created         *float64 `json:"created"`
	Modified        *float64 `json:"modified"`
	MessageCount    int      `json:"messageCount"`
	FirstMessage    string   `json:"firstMessage"`
	AllMessagesText string   `json:"allMessagesText"`
}

// TestListSessionsMatchesPi compares SessionManager.list of Pi 1.0.4 with ListCurrentSessions over a directory of session files that exercise the summary: message counting and text extraction, activity times against header and file times, names, header shapes, leading and malformed lines, and the file order of equal activity times.
func TestListSessionsMatchesPi(t *testing.T) {
	work := t.TempDir()
	cwd := filepath.Join(work, "proj")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	hdr := func(extra string) string {
		return `{"type":"session","version":3,"id":"s","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(cwd) + extra + `}` + "\n"
	}
	msg := func(role, content, ts string, extra string) string {
		return `{"type":"message","id":"m","parentId":null,"timestamp":"` + ts + `","message":{"role":"` + role + `","content":` + content + extra + `}}` + "\n"
	}
	const t1 = "2026-01-02T00:00:00.000Z"
	files := map[string]string{
		"a-basic.jsonl":       hdr("") + msg("user", `"hello"`, t1, "") + msg("assistant", `[{"type":"text","text":"hi"},{"type":"thinking","thinking":"x"},{"type":"text","text":"there"}]`, "2026-01-03T00:00:00.000Z", ""),
		"b-numts.jsonl":       hdr("") + msg("user", `"x"`, t1, `,"timestamp":1767400000000`) + msg("assistant", `"y"`, t1, `,"timestamp":1767300000000`),
		"c-zero.jsonl":        hdr("") + msg("user", `"x"`, "bad", `,"timestamp":0`),
		"d-negative.jsonl":    hdr("") + msg("user", `"x"`, "bad", `,"timestamp":-5`),
		"e-badtime.jsonl":     hdr("") + msg("user", `"x"`, "bad", ""),
		"f-nomsg.jsonl":       hdr(""),
		"g-toolresult.jsonl":  hdr("") + msg("toolResult", `"tool out"`, t1, "") + msg("user", `"u"`, t1, ""),
		"h-assistant.jsonl":   hdr("") + msg("assistant", `"only"`, t1, ""),
		"i-empty-text.jsonl":  hdr("") + msg("user", `""`, t1, "") + msg("user", `[{"type":"image","data":"x"}]`, t1, "") + msg("user", `"real"`, t1, ""),
		"j-nocontent.jsonl":   hdr("") + `{"type":"message","id":"m","parentId":null,"timestamp":"` + t1 + `","message":{"role":"user"}}` + "\n" + msg("user", `"after"`, t1, ""),
		"k-norole.jsonl":      hdr("") + `{"type":"message","id":"m","parentId":null,"timestamp":"` + t1 + `","message":{"content":"x"}}` + "\n",
		"l-name.jsonl":        hdr("") + `{"type":"session_info","id":"n","parentId":null,"timestamp":"` + t1 + `","name":"  My name  "}` + "\n" + msg("user", `"x"`, t1, ""),
		"m-nameclear.jsonl":   hdr("") + `{"type":"session_info","id":"n","parentId":null,"timestamp":"` + t1 + `","name":"A"}` + "\n" + `{"type":"session_info","id":"n2","parentId":null,"timestamp":"` + t1 + `","name":"   "}` + "\n",
		"n-namenum.jsonl":     hdr("") + `{"type":"session_info","id":"n","parentId":null,"timestamp":"` + t1 + `","name":5}` + "\n",
		"o-nonamefield.jsonl": hdr("") + `{"type":"session_info","id":"n","parentId":null,"timestamp":"` + t1 + `"}` + "\n",
		"p-parent.jsonl":      hdr(`,"parentSession":"/some/parent.jsonl"`),
		"q-parentnum.jsonl":   hdr(`,"parentSession":5`),
		"r-nocwd.jsonl":       `{"type":"session","version":3,"id":"s","timestamp":"2026-01-01T00:00:00.000Z"}` + "\n",
		"s-othercwd.jsonl":    `{"type":"session","version":3,"id":"s","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/elsewhere"}` + "\n",
		"t-noid.jsonl":        `{"type":"session","version":3,"timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"u-idnum.jsonl":       `{"type":"session","version":3,"id":7,"timestamp":"2026-01-01T00:00:00.000Z","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"v-msgfirst.jsonl":    msg("user", `"x"`, t1, "") + hdr(""),
		"w-empty.jsonl":       "",
		"x-garbage.jsonl":     "garbage\n\n   \n" + hdr("") + "{bad\n" + msg("user", `"ok"`, t1, "") + "null\n",
		"y-crlf.jsonl":        strings.ReplaceAll(hdr("")+msg("user", `"crlf"`, t1, ""), "\n", "\r\n"),
		"z-notimestamp.jsonl": `{"type":"session","version":3,"id":"s","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"A-ts-date.jsonl":     `{"type":"session","version":3,"id":"s","timestamp":"2026-02-03","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"B-ts-local.jsonl":    `{"type":"session","version":3,"id":"s","timestamp":"2026-02-03T04:05:06","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"C-ts-offset.jsonl":   `{"type":"session","version":3,"id":"s","timestamp":"2026-02-03T04:05:06.789+02:00","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"D-ts-micro.jsonl":    `{"type":"session","version":3,"id":"s","timestamp":"2026-02-03T04:05:06.123456Z","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"E-ts-text.jsonl":     `{"type":"session","version":3,"id":"s","timestamp":"Feb 3 2026 04:05:06 GMT","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"F-ts-garbage.jsonl":  `{"type":"session","version":3,"id":"s","timestamp":"garbage","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"G-ts-number.jsonl":   `{"type":"session","version":3,"id":"s","timestamp":1767400000000,"cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"H-ts-lowerz.jsonl":   `{"type":"session","version":3,"id":"s","timestamp":"2026-02-03t04:05:06z","cwd":` + strconv.Quote(cwd) + `}` + "\n",
		"I-tie-1.jsonl":       hdr("") + msg("user", `"tie"`, t1, `,"timestamp":1767500000000`),
		"J-tie-2.jsonl":       hdr("") + msg("user", `"tie"`, t1, `,"timestamp":1767500000000`),
		"K-tie-3.jsonl":       hdr("") + msg("user", `"tie"`, t1, `,"timestamp":1767500000000`),
		"l-tie-4.jsonl":       hdr("") + msg("user", `"tie"`, t1, `,"timestamp":1767500000000`),
		"é-tie-5.jsonl":       hdr("") + msg("user", `"tie"`, t1, `,"timestamp":1767500000000`),
		"note.txt":            hdr(""),
	}
	maps.Copy(files, randomSessionListFiles(cwd))
	dirs := [2]string{filepath.Join(work, "pig"), filepath.Join(work, "pi")}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		i := 0
		for name, content := range files {
			p := filepath.Join(d, name)
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			when := time.Unix(1_800_000_000+int64(i%7)*60, 0)
			i++
			if err := os.Chtimes(p, when, when); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Every file gets the same mtime pattern in both directories only if the map order matches, which it does not: fix the mtimes by name.
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	for i, name := range sortedStrings(names) {
		for _, d := range dirs {
			when := time.Unix(1_800_000_000+int64(i%7)*60, 0)
			if err := os.Chtimes(filepath.Join(d, name), when, when); err != nil {
				t.Fatal(err)
			}
		}
	}
	var want []listedSession
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/session-manager.js");
const path = await import("node:path");
const infos = await mod.SessionManager.list(input.cwd, input.dir);
emit(infos.map((s) => ({ file: path.basename(s.path), id: typeof s.id === "string" ? s.id : s.id === undefined ? "" : "<non-string>", cwd: s.cwd, name: s.name ?? "", parent: typeof s.parentSessionPath === "string" ? s.parentSessionPath : s.parentSessionPath === undefined ? "" : "<non-string>", created: Number.isNaN(s.created.getTime()) ? null : s.created.getTime(), modified: Number.isNaN(s.modified.getTime()) ? null : s.modified.getTime(), messageCount: s.messageCount, firstMessage: s.firstMessage, allMessagesText: s.allMessagesText })));`, map[string]string{"cwd": cwd, "dir": dirs[1]}, &want)
	infos, err := NewSessionManagerWithDir(cwd, dirs[0]).ListCurrentSessions()
	if err != nil {
		t.Fatal(err)
	}
	ms := func(t time.Time) *float64 {
		if t.IsZero() {
			return nil
		}
		v := float64(t.UnixMilli()) + 0
		return &v
	}
	// Pi hands back a non-string header id or parentSession as it is; SessionInfo holds strings, so those read as empty.
	for i := range want {
		if want[i].ID == "<non-string>" {
			want[i].ID = ""
		}
		if want[i].Parent == "<non-string>" {
			want[i].Parent = ""
		}
	}
	got := make([]listedSession, len(infos))
	for i, s := range infos {
		got[i] = listedSession{filepath.Base(s.Path), s.ID, s.CWD, s.Name, s.ParentSessionPath, ms(s.Created), ms(s.Modified), s.MessageCount, s.FirstMessage, s.AllMessagesText}
	}
	if len(got) != len(want) {
		t.Errorf("%d sessions, Pi %d", len(got), len(want))
	}
	for i := 0; i < len(got) && i < len(want); i++ {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("session %d:\n  Pig %s %s", i, describeListed(got[i]), "\n  Pi  "+describeListed(want[i]))
		}
	}
}

// randomSessionListFiles draws session files with a header of random shape (id, cwd, parent and timestamp of any type), then random lines: messages of every role with string, block-array, null or wrong-typed content and any timestamp spelling, name entries, other entry types, garbage, blank lines, a byte order mark and CRLF endings.
func randomSessionListFiles(cwd string) map[string]string {
	rng := rand.New(rand.NewSource(20261020))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	files := map[string]string{}
	for n := range 300 {
		var b strings.Builder
		if rng.Intn(25) == 0 {
			b.WriteString("\ufeff")
		}
		header := `{"type":"session","version":3`
		if rng.Intn(8) != 0 {
			header += `,"id":` + pick(`"s"`, `"s"`, `7`, `null`, `""`, `"日本"`)
		}
		if rng.Intn(10) != 0 {
			header += `,"timestamp":` + pick(`"2026-01-01T00:00:00.000Z"`, `"2026-02-03"`, `"2026-02-03T04:05:06"`, `"2026-02-03T04:05:06.789+02:00"`, `"garbage"`, `1767400000000`, `null`, `""`)
		}
		header += `,"cwd":` + pick(strconv.Quote(cwd), strconv.Quote(cwd), strconv.Quote(cwd), `"/elsewhere"`, `5`, `null`)
		if rng.Intn(5) == 0 {
			header += `,"parentSession":` + pick(`"/p/parent.jsonl"`, `5`, `null`, `""`)
		}
		header += "}"
		lines := []string{}
		for range rng.Intn(8) {
			ts := pick(`"2026-01-02T00:00:00.000Z"`, `"2026-01-03T10:20:30.123Z"`, `"bad"`, `5`, `null`, `"2026-01-02"`)
			content := pick(`"hello"`, `""`, `"  padded  "`, `"multi\nline\ttext"`, `[{"type":"text","text":"block one"},{"type":"text","text":"two"}]`, `[{"type":"thinking","thinking":"t"},{"type":"text","text":"after"}]`,
				`[{"type":"image","data":"x"}]`, `[{"type":"text"}]`, `[{"type":"text","text":5}]`, `[]`, `null`, `5`, `{"a":1}`, `"日本語 😀"`, `[{"type":"toolCall","id":"c","name":"n","arguments":{}}]`)
			switch rng.Intn(10) {
			case 0, 1, 2, 3:
				role := pick("user", "assistant", "toolResult", "system", "custom", "", "bashExecution")
				messageTimestamp := ""
				if rng.Intn(2) == 0 {
					messageTimestamp = `,"timestamp":` + pick(`1767400000000`, `1767300000000.5`, `0`, `-5`, `"1767400000000"`, `null`, `1e15`)
				}
				lines = append(lines, `{"type":"message","id":"m`+strconv.Itoa(rng.Intn(5))+`","parentId":null,"timestamp":`+ts+`,"message":{"role":"`+role+`","content":`+content+messageTimestamp+`}}`)
			case 4:
				lines = append(lines, `{"type":"session_info","id":"n","parentId":null,"timestamp":`+ts+`,"name":`+pick(`"  Name  "`, `""`, `"   "`, `5`, `null`, `"日本"`, `"a\tb"`)+`}`)
			case 5:
				lines = append(lines, `{"type":"model_change","id":"mc","parentId":null,"timestamp":`+ts+`,"provider":"p","modelId":"m"}`)
			case 6:
				lines = append(lines, pick("garbage", "{bad", "null", "[]", "5", `"str"`, "", "   ", `{"type":"message"}`, `{"type":"message","message":null}`))
			case 7:
				lines = append(lines, `{"type":"custom_message","id":"c","parentId":null,"timestamp":`+ts+`,"customType":"x","content":`+content+`,"display":true}`)
			default:
				lines = append(lines, `{"type":"message","id":"m","parentId":null,"timestamp":`+ts+`,"message":{"role":"user","content":`+content+`}}`)
			}
		}
		all := append([]string{header}, lines...)
		if rng.Intn(15) == 0 && len(all) > 1 {
			all[0], all[1] = all[1], all[0]
		}
		eol := "\n"
		if rng.Intn(10) == 0 {
			eol = "\r\n"
		}
		b.WriteString(strings.Join(all, eol))
		if rng.Intn(4) != 0 {
			b.WriteString(eol)
		}
		files[fmt.Sprintf("r%03d.jsonl", n)] = b.String()
	}
	return files
}

func sortedStrings(in []string) []string {
	out := append([]string{}, in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func describeListed(s listedSession) string {
	f := func(p *float64) string {
		if p == nil {
			return "nil"
		}
		return strconv.FormatFloat(*p, 'f', -1, 64)
	}
	return s.File + " id=" + s.ID + " cwd=" + s.CWD + " name=" + strconv.Quote(s.Name) + " parent=" + s.Parent + " created=" + f(s.Created) + " modified=" + f(s.Modified) + " n=" + strconv.Itoa(s.MessageCount) + " first=" + strconv.Quote(s.FirstMessage) + " all=" + strconv.Quote(s.AllMessagesText)
}
