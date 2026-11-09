package host

// pi: packages/chord/test/examples/24-keyed-members.ts

import (
	"context"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// --- 24-keyed-members.ts -------------------------------------------------------------------------------------------

func TestExample24KeyedMembers(t *testing.T) {
	profile := exObj(exProp("name", exStr()), exProp("status", exStr()))
	profiles := exDefineState("profiles", documents.StateOptions{
		Schema:  profile,
		Initial: func() *delta.JsonObject { return jsonObject("name", "", "status", "offline") },
		Keyed:   true,
	})
	// The directory: every user's profile, one keyed member.
	directory := exLocal("example.directory", contracts.Params{},
		contracts.Mem("profiles", contracts.Keyed(profile)),
		contracts.Mem("set", contracts.NewMethod(exObj(exProp("user", exStr()), exProp("name", exStr()), exProp("status", exStr())))))
	// A badge per user: its own object, showing one profile.
	badge := exLocal("example.badge", contracts.Params{exProp("user", exStr())}, contracts.Mem("label", exStr()))
	host, _ := exOpenHost(t, loadedOptions{}, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(directory, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			handle := self.State(profiles).(*documents.Keyed)
			// The keyed handle is the member as it is.
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Keyed: map[string]contracts.KeyedWatchable[delta.JsonValue]{"profiles": ExposeKeyed(handle)},
				Methods: map[string]contracts.MethodFunction{
					"set": exMethod(func(args *delta.JsonObject, ctx context.Context) (delta.JsonValue, error) {
						_, err := handle.Change(ctx, exParam(args, "user"), func(draft *delta.Object) (any, error) {
							if err := draft.Set("name", args.Value("name")); err != nil {
								return nil, err
							}
							return nil, draft.Set("status", args.Value("status"))
						})
						return nil, err
					}),
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(badge, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
			user := exParam(params, "user")
			// Pinned for this attempt; the badge goes live once the profile is loaded.
			profile, err := self.Get(directory, jsonObject()).Keyed("profiles").At(user)
			if err != nil {
				return contracts.Implementation{}, err
			}
			return exImpl(map[string]chord.Watchable[delta.JsonValue]{
				"label": self.Derive([]any{profile}, func(values []any) (delta.JsonValue, error) {
					entry := values[0].(*delta.JsonObject)
					name, _ := entry.Value("name").(string)
					if name == "" {
						name = user
					}
					return fmt.Sprintf("%s (%v)", name, entry.Value("status")), nil
				}),
			}, nil), nil
		}, ServeOptions{})
	})
	dir := exGet(t, host, directory, jsonObject())
	exCall(t, dir, "set", jsonObject("user", "ana", "name", "Ana", "status", "online"))
	badgeRef := exGet(t, host, badge, jsonObject("user", "ana"))
	exEqual(t, "badge", exValue(t, badgeRef, "label"), "Ana (online)")
	exCall(t, dir, "set", jsonObject("user", "ana", "name", "Ana", "status", "away"))
	exEqual(t, "badge after a change", exValue(t, badgeRef, "label"), "Ana (away)")
	// Any key, read where it is needed.
	_, found, err := dir.Keyed("profiles").Get(background, "bo")
	must(t, err)
	if found {
		t.Fatal("ref.profiles.get(bo): found")
	}
	badgeRef.Release()
	exCloseHost(t, host)
}

// exBytesSource is a store.ByteSource over a file's contents, in chunks (`createReadStream`).
type exBytesSource struct {
	content []byte
	chunk   int
}

func (s *exBytesSource) Next(context.Context) ([]byte, bool, error) {
	if len(s.content) == 0 {
		return nil, false, nil
	}
	n := min(s.chunk, len(s.content))
	chunk := s.content[:n]
	s.content = s.content[n:]
	return chunk, true, nil
}

func (s *exBytesSource) Close() error { return nil }
