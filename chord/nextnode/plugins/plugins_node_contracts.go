package plugins

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Contracts as data (docs/plan/chord-plugins-node-evaluator.md 10.2). No Pi file corresponds to this one: a type crosses the
// process boundary between a Go host and a Node host as the normal form its fingerprint hashes, and the other side declares
// its own contract of it (node/contracts.mjs, `contractOf`, with Pi's `local` and `remote`).

// ContractData is a contract as it crosses to a Node host: its name, its current version and fingerprint, and Normal, the
// normal form the fingerprint hashes (`{ kind, params, members }` for a local contract, `{ kind, params, variants }` for a
// remote one: contracts.NormalForm). The Node host rebuilds the contract from it and refuses one that does not hash to
// Fingerprint. A remote contract's `HostAddress` is a Go function and does not cross; the history of an evolved contract
// does not either (the version a holder holds is the one that crosses).
type ContractData struct {
	Name        string            `json:"name"`
	Version     int               `json:"version"`
	Fingerprint string            `json:"fingerprint"`
	Normal      *delta.JsonObject `json:"normal"`
}

// ContractDataOf is the ContractData of contract's current version. It fails for a value that is not a declared contract
// (contracts.ContractDataOf).
func ContractDataOf(contract *contracts.Contract) (*ContractData, error) {
	declared, err := contracts.ContractDataOf(contract)
	if err != nil {
		return nil, err
	}
	current := declared.Versions[len(declared.Versions)-1]
	return &ContractData{
		Name: declared.Name, Version: current.Version, Fingerprint: current.Fingerprint,
		Normal: contracts.NormalForm(declared.Kind, declared.Params, current.Variants),
	}, nil
}

// ContractDescription is a contract as the Node host rebuilt it from ContractData: its kind, name, version and fingerprint as
// that host's Pi computes them.
type ContractDescription struct {
	Kind        contracts.Kind `json:"kind"`
	Name        string         `json:"name"`
	Version     int            `json:"version"`
	Fingerprint string         `json:"fingerprint"`
}

// describeRequest is the `describe` control request of bridge.mjs.
type describeRequest struct {
	envelope
	Contract *ContractData `json:"contract"`
}

// Describe is what the Node host makes of contract: the contract it rebuilds from ContractDataOf(contract), described with
// the fingerprint its own Pi hashes the rebuilt declaration to. It equals contract's own when the two sides agree on the
// normal form; the Node host's `get` refuses a contract whose rebuilt fingerprint differs. It blocks until the answer.
func (h *NodeHost) Describe(contract *contracts.Contract, ctx context.Context) (ContractDescription, error) {
	data, err := ContractDataOf(contract)
	if err != nil {
		return ContractDescription{}, err
	}
	call, err := h.send("describe", &describeRequest{Contract: data})
	if err != nil {
		return ContractDescription{}, err
	}
	reply, err := h.await(call, ctx)
	if err != nil {
		return ContractDescription{}, err
	}
	var description ContractDescription
	if err := json.Unmarshal(reply.Result, &description); err != nil {
		return ContractDescription{}, err
	}
	return description, nil
}
