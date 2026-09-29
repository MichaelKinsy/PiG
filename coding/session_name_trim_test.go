package coding

import "testing"

func TestSessionSetNameUsesJavaScriptTrim(t *testing.T) {
	// The /name wrapper uses the same ECMAScript blank-name test as rpc-mode.ts:662-664; session-manager.ts:1303 trims the persisted name as well.
	for _, tc := range []struct {
		input, want string
		rejected    bool
	}{
		{input: " \ufeff\t", rejected: true},
		{input: " \u0085 ", want: "\u0085"},
		{input: "\ufeffname\ufeff", want: "name"},
		{input: "\u0085name\u0085", want: "\u0085name\u0085"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sess.Close() }()
			err = sess.SetName(tc.input)
			if (err != nil) != tc.rejected {
				t.Fatalf("SetName(%q) error=%v, rejected=%v", tc.input, err, tc.rejected)
			}
			if !tc.rejected {
				flushSess(t, sess)
				infos, err := sess.ListSessions()
				if err != nil || len(infos) != 1 || infos[0].Name != tc.want {
					t.Fatalf("persisted names=%+v err=%v want=%q", infos, err, tc.want)
				}
			}
		})
	}
}
