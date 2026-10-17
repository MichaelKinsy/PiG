// SPDX-License-Identifier: MIT

package spec

import (
	"bytes"
	"encoding/json"
)

type jsonKind struct {
	Name   string `json:"name"`
	Number uint8  `json:"number"`
	Layout string `json:"layout"`
}

type jsonAPI struct {
	Op       string `json:"op"`
	Args     string `json:"args"`
	Result   string `json:"result"`
	Answered string `json:"answered"`
}

type jsonExport struct {
	Name      string `json:"name"`
	Signature string `json:"signature"`
}

type jsonTables struct {
	ID        string       `json:"id"`
	Exports   []jsonExport `json:"exports"`
	Imports   []jsonExport `json:"imports"`
	Values    []jsonKind   `json:"values"`
	Status    []jsonKind   `json:"status"`
	Events    []jsonKind   `json:"events"`
	Effects   []jsonKind   `json:"effects"`
	Notices   []jsonKind   `json:"notices"`
	API       []jsonAPI    `json:"api"`
	Canonical string       `json:"canonical"`
}

func kinds(rows []kindRow) []jsonKind {
	out := make([]jsonKind, len(rows))
	for i, r := range rows {
		out[i] = jsonKind(r)
	}
	return out
}

func exports(rows []exportRow) []jsonExport {
	out := make([]jsonExport, len(rows))
	for i, r := range rows {
		out[i] = jsonExport(r)
	}
	return out
}

// JSON renders the tables and ID as the abi.json the shim imports.
func JSON() ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "\t")
	err := enc.Encode(jsonTables{
		ID: ID(), Exports: exports(exportRows), Imports: exports(importRows), Values: kinds(valueRows), Status: kinds(statusRows),
		Events: kinds(eventRows), Effects: kinds(effectRows), Notices: kinds(noticeRows), API: apiJSON(), Canonical: Canonical(),
	})
	return out.Bytes(), err
}

func apiJSON() []jsonAPI {
	out := make([]jsonAPI, len(apiRows))
	for i, r := range apiRows {
		out[i] = jsonAPI(r)
	}
	return out
}
