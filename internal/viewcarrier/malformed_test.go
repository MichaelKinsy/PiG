package viewcarrier

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// TestCarrierMalformedCommandAnswers pins, byte for byte, the line the carrier writes for a command line that is not
// what the command's members say: an unknown member, a member of the wrong type, data after the command, a number no
// float64 holds, and null where an object is expected. A client tells these apart by the text, and what the carrier
// accepts (data after the command, any JSON value as the id) is as much the wire as what it refuses. $A is the
// fixture's abilities and $N its attachment; every case starts from a fresh attached connection with one mount.
func TestCarrierMalformedCommandAnswers(t *testing.T) {
	for _, tc := range []struct{ name, id, command, line, want string }{
		{"unknown member", `"u1"`, "ui_view_attach", `{"id":"u1","type":"ui_view_attach","abilities":$A,"version":1}`,
			`{"id":"u1","type":"response","command":"ui_view_attach","success":false,"error":"invalid: unknown field \"version\"","code":"invalid"}`},
		{"unknown nested member", `"u2"`, "ui_view_report", `{"id":"u2","type":"ui_view_report","attachment":$N,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"undrawn","severity":"info","count":2}}`,
			`{"id":"u2","type":"response","command":"ui_view_report","success":false,"error":"invalid: unknown field \"count\"","code":"invalid"}`},
		{"string for a number", `"w1"`, "ui_view_resync", `{"id":"w1","type":"ui_view_resync","attachment":"$N"}`,
			`{"id":"w1","type":"response","command":"ui_view_resync","success":false,"error":"invalid: cannot unmarshal string into Go struct field .attachment of type uint64","code":"invalid"}`},
		{"negative for an unsigned number", `"w2"`, "ui_view_resync", `{"id":"w2","type":"ui_view_resync","attachment":-1}`,
			`{"id":"w2","type":"response","command":"ui_view_resync","success":false,"error":"invalid: cannot unmarshal number -1 into Go struct field .attachment of type uint64","code":"invalid"}`},
		{"fraction for an integer", `"w3"`, "ui_view_resync", `{"id":"w3","type":"ui_view_resync","attachment":1.5}`,
			`{"id":"w3","type":"response","command":"ui_view_resync","success":false,"error":"invalid: cannot unmarshal number 1.5 into Go struct field .attachment of type uint64","code":"invalid"}`},
		{"number for a string", `"w4"`, "ui_view_intent", `{"id":"w4","type":"ui_view_intent","attachment":$N,"plugin":7,"mount":{"key":"k","epoch":1,"rev":1},"intent":"add","arg":{}}`,
			`{"id":"w4","type":"response","command":"ui_view_intent","success":false,"error":"invalid: cannot unmarshal number into Go struct field .plugin of type string","code":"invalid"}`},
		{"array for an object", `"w5"`, "ui_view_attach", `{"id":"w5","type":"ui_view_attach","abilities":[]}`,
			`{"id":"w5","type":"response","command":"ui_view_attach","success":false,"error":"invalid: cannot unmarshal array into Go struct field .abilities of type uiview.ClientAbilities","code":"invalid"}`},
		{"array for the type", `"w6"`, "ui_view_detach", `{"id":"w6","type":["ui_view_detach"],"attachment":$N}`,
			`{"id":"w6","type":"response","command":"ui_view_detach","success":false,"error":"invalid: cannot unmarshal array into Go struct field .type of type string","code":"invalid"}`},
		{"object id is carried", `{"a":1}`, "ui_view_detach", `{"id":{"a":1},"type":"ui_view_detach","attachment":$N}`,
			`{"id":{"a":1},"type":"response","command":"ui_view_detach","success":true,"data":{}}`},
		{"trailing object", `"t1"`, "ui_view_detach", `{"id":"t1","type":"ui_view_detach","attachment":$N} {"x":1}`,
			`{"id":"t1","type":"response","command":"ui_view_detach","success":true,"data":{}}`},
		{"trailing text", `"t2"`, "ui_view_detach", `{"id":"t2","type":"ui_view_detach","attachment":$N} garbage`,
			`{"id":"t2","type":"response","command":"ui_view_detach","success":true,"data":{}}`},
		{"trailing bracket", `"t3"`, "ui_view_detach", `{"id":"t3","type":"ui_view_detach","attachment":$N}]`,
			`{"id":"t3","type":"response","command":"ui_view_detach","success":true,"data":{}}`},
		{"truncated", `"t4"`, "ui_view_detach", `{"id":"t4","type":"ui_view_detach","attachment":`,
			`{"id":"t4","type":"response","command":"ui_view_detach","success":false,"error":"invalid: unexpected EOF","code":"invalid"}`},
		{"empty line", `"t5"`, "ui_view_detach", ``,
			`{"id":"t5","type":"response","command":"ui_view_detach","success":false,"error":"invalid: EOF","code":"invalid"}`},
		{"null line", `"t6"`, "ui_view_detach", `null`,
			`{"id":"t6","type":"response","command":"ui_view_detach","success":false,"error":"invalid: attachment is required","code":"invalid"}`},
		{"array line", `"t7"`, "ui_view_detach", `[]`,
			`{"id":"t7","type":"response","command":"ui_view_detach","success":false,"error":"invalid: cannot unmarshal array into Go value of type struct { ID jsontext.Value \"json:\\\"id\\\"\"; Type string \"json:\\\"type\\\"\"; Attachment *uint64 \"json:\\\"attachment\\\"\" }","code":"invalid"}`},
		{"big integer id", `12345678901234567890123`, "ui_view_detach", `{"id":12345678901234567890123,"type":"ui_view_detach","attachment":$N}`,
			`{"id":12345678901234567890123,"type":"response","command":"ui_view_detach","success":true,"data":{}}`},
		{"big integer id of a refused command", `12345678901234567890123`, "ui_view_resync", `{"id":12345678901234567890123,"type":"ui_view_resync","since":1}`,
			`{"id":12345678901234567890123,"type":"response","command":"ui_view_resync","success":false,"error":"invalid: unknown field \"since\"","code":"invalid"}`},
		{"id beyond float64", `1e400`, "ui_view_detach", `{"id":1e400,"type":"ui_view_detach","attachment":$N}`,
			`{"id":1e400,"type":"response","command":"ui_view_detach","success":true,"data":{}}`},
		{"attachment beyond uint64", `"b3"`, "ui_view_resync", `{"id":"b3","type":"ui_view_resync","attachment":18446744073709551616}`,
			`{"id":"b3","type":"response","command":"ui_view_resync","success":false,"error":"invalid: cannot unmarshal number 18446744073709551616 into Go struct field .attachment of type uint64","code":"invalid"}`},
		{"attachment with an exponent", `"b4"`, "ui_view_resync", `{"id":"b4","type":"ui_view_resync","attachment":1e0}`,
			`{"id":"b4","type":"response","command":"ui_view_resync","success":false,"error":"invalid: cannot unmarshal number 1e0 into Go struct field .attachment of type uint64","code":"invalid"}`},
		{"big integer in an intent arg", `"b5"`, "ui_view_intent", `{"id":"b5","type":"ui_view_intent","attachment":$N,"plugin":"p","mount":{"key":"k","epoch":1,"rev":1},"intent":"add","arg":{"n":12345678901234567890123}}`,
			`{"id":"b5","type":"response","command":"ui_view_intent","success":true,"data":{"ticket":"","outcome":"refused","error":"value has unknown member \"n\""}}`},
		{"measure beyond int64", `"b6"`, "ui_view_report", `{"id":"b6","type":"ui_view_report","attachment":$N,"plugin":"p","mount":{"key":"k","epoch":1},"notice":{"code":"undrawn","severity":"info","measured":9223372036854775808}}`,
			`{"id":"b6","type":"response","command":"ui_view_report","success":false,"error":"invalid: cannot unmarshal number 9223372036854775808 into Go struct field .notice.measured of type int64","code":"invalid"}`},
		{"null abilities", `"n1"`, "ui_view_attach", `{"id":"n1","type":"ui_view_attach","abilities":null}`,
			`{"id":"n1","type":"response","command":"ui_view_attach","success":false,"error":"invalid: abilities is required","code":"invalid"}`},
		{"null mount", `"n2"`, "ui_view_intent", `{"id":"n2","type":"ui_view_intent","attachment":$N,"plugin":"p","mount":null,"intent":"add","arg":{}}`,
			`{"id":"n2","type":"response","command":"ui_view_intent","success":false,"error":"invalid: plugin, mount, intent and arg are required","code":"invalid"}`},
		{"null arg", `"n3"`, "ui_view_intent", `{"id":"n3","type":"ui_view_intent","attachment":$N,"plugin":"p","mount":{"key":"k","epoch":1,"rev":1},"intent":"add","arg":null}`,
			`{"id":"n3","type":"response","command":"ui_view_intent","success":true,"data":{"ticket":"","outcome":"refused","error":"value is not an object"}}`},
		{"null notice", `"n4"`, "ui_view_report", `{"id":"n4","type":"ui_view_report","attachment":$N,"plugin":"p","mount":{"key":"k","epoch":1},"notice":null}`,
			`{"id":"n4","type":"response","command":"ui_view_report","success":false,"error":"invalid: attachment, plugin, mount and notice are required","code":"invalid"}`},
		{"null attachment", `"n5"`, "ui_view_resync", `{"id":"n5","type":"ui_view_resync","attachment":null}`,
			`{"id":"n5","type":"response","command":"ui_view_resync","success":false,"error":"invalid: attachment is required","code":"invalid"}`},
		{"null nested object", `"n6"`, "ui_view_abilities", `{"id":"n6","type":"ui_view_abilities","attachment":$N,"abilities":{"slots":["panel"],"limits":null}}`,
			`{"id":"n6","type":"response","command":"ui_view_abilities","success":true,"data":{}}`},
		{"null id", `null`, "ui_view_detach", `{"id":null,"type":"ui_view_detach","attachment":$N}`,
			`{"id":null,"type":"response","command":"ui_view_detach","success":true,"data":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			n := f.attach(4096)
			f.mount("k", "one", intentDecls)
			f.settled()
			line := strings.NewReplacer("$A", abilitiesJSON(4096), "$N", n).Replace(tc.line)
			answers := make(chan string, 1)
			f.c.Handle(json.RawMessage(tc.id), tc.command, []byte(line), func(response any, written func()) {
				raw, err := jsstring.MarshalJSON(response)
				if err != nil {
					t.Errorf("reply %+v: %v", response, err)
				}
				answers <- string(raw)
				if written != nil {
					written()
				}
			})
			select {
			case got := <-answers:
				if got != tc.want {
					t.Errorf("%s\nGOT\t%s\t%s\nwant %s", line, tc.name, got, tc.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: no answer", line)
			}
		})
	}
}
