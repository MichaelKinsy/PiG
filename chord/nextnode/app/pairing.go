package app

import (
	"crypto/rand"
	"math/big"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Device pairing (slice 13 §2, decision "The app kit, point 3" (c)): a command-line or terminal client on another machine asks for
// a code (`POST /_chord/pair`, public) and polls with a secret it got with it; the owner (the principal the app names) opens
// `/_chord/pair/<code>` in a signed-in browser and approves; the next poll returns a token of the owner's new device session,
// once. Codes are short, random, single use and expire after 10 minutes; the poll secret is kept only hashed. Asking is public, so
// pairings anonymous clients start are capped: MaxPending in all (100 by default) and PerAddress per client address (10). The
// trade: a party filling the caps locks pairing out for others for up to 10 minutes (behind a tunnel every remote client has the
// tunnel's address), instead of growing the server's memory without bound; a signed-in owner's pairings are not counted, so the
// owner can always pair.
//
// Ports packages/chord/src/node/app/pairing.ts (Pi main c23b9ec1). Forced Go differences (D459): a starter is a struct (Pi's tagged
// union), `begin`, `poll` and `waiting` answer a second `ok` value where Pi's answer `undefined`, and a pairing is approved by
// its Approve method (Pi's route sets `pairing.token` on the shared object).

const (
	// pairingMs is how long a pairing code is valid: 10 minutes.
	pairingMs = 10 * 60_000
	// pairingAlphabet is the code's letters: no 0/O, 1/I/L.
	pairingAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	// PairPath is the pairing routes' prefix.
	PairPath = "/_chord/pair"
)

// PairingStarter is who starts a pairing: the signed-in owner (not counted), or an anonymous client at Address (counted).
type PairingStarter struct {
	Owner   bool
	Address string
}

// Pairing is one pairing under way: its code, the device's label, who started it, its expiry and its token once approved.
type Pairing struct {
	Code    string
	Label   string
	By      PairingStarter
	expires float64
	owner   *Pairings
	token   string
	// approved is whether token is set (a token is never empty, but the state is explicit).
	approved bool
}

// Approve gives the pairing its device session's token: the device's next poll gets it.
func (p *Pairing) Approve(token string) {
	p.owner.mu.Lock()
	defer p.owner.mu.Unlock()
	p.token, p.approved = token, true
}

// PairingLimits are the limits of pairings under way: in all (100 by default) and per client address (10 by default); zero is the
// default.
type PairingLimits struct {
	MaxPending int
	PerAddress int
}

// PollKind is what a poll learns.
type PollKind string

const (
	// PollWaiting: the owner has not approved yet.
	PollWaiting PollKind = "waiting"
	// PollApproved: the device's token, once.
	PollApproved PollKind = "approved"
)

// Polled is what a poll learned.
type Polled struct {
	Kind  PollKind
	Token string
}

// BegunPairing is a new pairing's code (for the owner) and poll secret (for the device).
type BegunPairing struct{ Code, Poll string }

// Pairings are the pairings under way, by code and by their poll secret's hash, with the anonymous ones counted.
type Pairings struct {
	mu            sync.Mutex
	byCode        map[string]*Pairing
	byPoll        map[string]*Pairing
	perAddress    map[string]int
	anonymous     int
	maxPending    int
	maxPerAddress int
}

// NewPairings is no pairings, with limits.
func NewPairings(limits PairingLimits) *Pairings {
	pairings := &Pairings{byCode: map[string]*Pairing{}, byPoll: map[string]*Pairing{}, perAddress: map[string]int{}, maxPending: 100, maxPerAddress: 10}
	if limits.MaxPending != 0 {
		pairings.maxPending = limits.MaxPending
	}
	if limits.PerAddress != 0 {
		pairings.maxPerAddress = limits.PerAddress
	}
	return pairings
}

// Begin is a new pairing for a device labelled label, started by the owner or an anonymous client; false when an anonymous client
// reached a limit.
func (p *Pairings) Begin(label string, by PairingStarter) (BegunPairing, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire()
	if !by.Owner {
		if p.anonymous >= p.maxPending || p.perAddress[by.Address] >= p.maxPerAddress {
			return BegunPairing{}, false
		}
		p.anonymous++
		p.perAddress[by.Address]++
	}
	var code string
	for {
		code = pairingLetters(4) + "-" + pairingLetters(4)
		if _, taken := p.byCode[code]; !taken {
			break
		}
	}
	poll := NewToken()
	pairing := &Pairing{Code: code, Label: label, By: by, expires: jsclock.DateNow() + pairingMs, owner: p}
	p.byCode[code] = pairing
	p.byPoll[HashToken(poll)] = pairing
	return BegunPairing{Code: code, Poll: poll}, true
}

// Waiting is the pairing waiting for approval under code (any case), unexpired.
func (p *Pairings) Waiting(code string) (*Pairing, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire()
	pairing, ok := p.byCode[strings.ToUpper(code)]
	if !ok || pairing.approved {
		return nil, false
	}
	return pairing, true
}

// Poll is what a poll with poll learns: still waiting, the device's token (once: the pairing ends), or nothing (false).
func (p *Pairings) Poll(poll string) (Polled, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire()
	hash := HashToken(poll)
	pairing, ok := p.byPoll[hash]
	if !ok {
		return Polled{}, false
	}
	if !pairing.approved {
		return Polled{Kind: PollWaiting}, true
	}
	p.end(hash, pairing)
	return Polled{Kind: PollApproved, Token: pairing.token}, true
}

// expire drops the expired pairings.
func (p *Pairings) expire() {
	now := jsclock.DateNow()
	for hash, pairing := range p.byPoll {
		if pairing.expires <= now {
			p.end(hash, pairing)
		}
	}
}

// end ends pairing (its poll secret's hash hash).
func (p *Pairings) end(hash string, pairing *Pairing) {
	delete(p.byPoll, hash)
	delete(p.byCode, pairing.Code)
	if pairing.By.Owner {
		return
	}
	p.anonymous--
	left := p.perAddress[pairing.By.Address] - 1
	if left <= 0 {
		delete(p.perAddress, pairing.By.Address)
	} else {
		p.perAddress[pairing.By.Address] = left
	}
}

// pairingLetters is count random letters of the alphabet (`crypto.randomInt`).
func pairingLetters(count int) string {
	var text strings.Builder
	limit := big.NewInt(int64(len(pairingAlphabet)))
	for range count {
		index, err := rand.Int(rand.Reader, limit)
		if err != nil {
			// crypto/rand does not fail on the platforms Go supports; a process without randomness cannot make codes.
			panic(err)
		}
		text.WriteByte(pairingAlphabet[index.Int64()])
	}
	return text.String()
}

// PairingFacet is the kit's facet for device pairing: `POST /_chord/pair` (public: `{ label? }` -> `{ code, poll }`, 429 past a limit
// unless the owner asks), `POST /_chord/pair/poll` (public: `{ poll }` -> `{ status: "waiting" }`, `{ status: "approved", token }`
// once, else 404), `GET` and `POST /_chord/pair/:code` (owner's confirm page and approval: a device session of owner).
func PairingFacet(pairings *Pairings, owner data.Principal) host.Facet {
	notFound := chordhttp.HttpResponse{Kind: chordhttp.KindHTML, Status: 404, Body: "<p>No such pairing (or it expired).</p>"}
	ownerRoute := func(approve bool) chordhttp.HttpRoute {
		return func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
			if !data.SamePrincipal(ctx.Principal(), owner, ctx) {
				return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 403}, nil
			}
			pairing, ok := pairings.Waiting(request.Params()["code"])
			if !ok {
				return notFound, nil
			}
			if !approve {
				return chordhttp.HttpResponse{Kind: chordhttp.KindHTML, Body: confirmPage(pairing.Code, pairing.Label)}, nil
			}
			issued, err := issueSession(ctx, owner, pairing.Label, SessionDevice, false)
			if err != nil {
				return chordhttp.HttpResponse{}, err
			}
			pairing.Approve(issued.token)
			return chordhttp.HttpResponse{Kind: chordhttp.KindHTML, Body: "<p>Approved: " + escapeHTML(pairing.Label) + " is signed in as the owner.</p>"}, nil
		}
	}
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](chordhttp.Http), chordhttp.HttpContribution{Routes: []chordhttp.HttpRouteEntry{
			{Key: "POST " + PairPath, Route: chordhttp.HttpRouteOptions{Public: true, Handler: func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
				label, err := pairingLabel(request)
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				by := PairingStarter{Address: request.RemoteAddress()}
				if data.SamePrincipal(ctx.Principal(), owner, ctx) {
					by = PairingStarter{Owner: true}
				}
				begun, ok := pairings.Begin(label, by)
				if !ok {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 429}, nil
				}
				return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: delta.JsonObjectOf("code", begun.Code, "poll", begun.Poll)}, nil
			}}},
			{Key: "POST " + PairPath + "/poll", Route: chordhttp.HttpRouteOptions{Public: true, Handler: func(request chordhttp.HttpRequest, _ host.Ctx) (chordhttp.HttpResponse, error) {
				// A trust boundary: anyone may poll.
				body, err := request.JSON()
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				object, _ := body.(*delta.JsonObject)
				var polled Polled
				var ok bool
				if object != nil {
					if poll, isText := object.Value("poll").(string); isText {
						polled, ok = pairings.Poll(poll)
					}
				}
				if !ok {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 404}, nil
				}
				if polled.Kind == PollWaiting {
					return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: delta.JsonObjectOf("status", "waiting")}, nil
				}
				return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: delta.JsonObjectOf("status", "approved", "token", polled.Token)}, nil
			}}},
			chordhttp.RouteFunc("GET "+PairPath+"/:code", ownerRoute(false)),
			chordhttp.RouteFunc("POST "+PairPath+"/:code", ownerRoute(true)),
		}})
	})
}

// pairingLabel is the device's label from a pairing request's body (`{ label }`, at most 100 characters), else `device`.
func pairingLabel(request chordhttp.HttpRequest) (string, error) {
	length, _ := chordhttp.HeaderGet(request.Headers(), "content-length")
	_, chunked := chordhttp.HeaderGet(request.Headers(), "transfer-encoding")
	if isZeroNumber(length) && !chunked {
		return "device", nil
	}
	// A trust boundary: anyone may ask for a code.
	body, err := request.JSON(4096)
	if err != nil {
		return "", err
	}
	if object, ok := body.(*delta.JsonObject); ok {
		if label, isText := object.Value("label").(string); isText && jsstring.Trim(label) != "" {
			return jsstring.Slice(jsstring.Trim(label), 0, 100), nil
		}
	}
	return "device", nil
}

// isZeroNumber is whether `Number(text)` is 0: no text (`Number("")`), or a numeral of value zero.
func isZeroNumber(text string) bool { return jsnumber.Parse(text) == 0 }

// confirmPage is the owner's confirm page for pairing code (a form posting to itself: same origin, the session cookie).
func confirmPage(code, label string) string {
	return strings.Join([]string{
		"<!doctype html><title>Pair a device</title>",
		"<p>Sign in <b>" + escapeHTML(label) + "</b> as the owner? Its code: <b>" + escapeHTML(code) + "</b> (compare it with the device).</p>",
		`<form method="post"><button>Approve</button></form>`,
	}, "\n")
}

var htmlEscaper = strings.NewReplacer("&", "&#38;", "<", "&#60;", ">", "&#62;", `"`, "&#34;", "'", "&#39;")

// escapeHTML is text safe in HTML.
func escapeHTML(text string) string { return htmlEscaper.Replace(text) }
