// Command genmanifest regenerates wago-plugin.json from the extensions this module
// provides. The manifest is package.json-style: per extension, its basic details,
// tags, repository, version, and engines — the metadata a registry or catalog reads
// without compiling. (Host-import signatures are deliberately left out; discover
// those by compiling or via `wago plugin inspect`.)
//
// Run from the module root:
//
//	go generate ./...            # or: go run ./internal/genmanifest
package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"

	wago "github.com/wago-org/wago"
	"github.com/wago-org/wasi/p1"
	"github.com/wago-org/wasi/unstable"
)

// manifest is the wago-plugin.json document: what extensions this module ships.
type manifest struct {
	Schema     string      `json:"schema"`
	Module     string      `json:"module"`
	Extensions []extension `json:"extensions"`
}

// extension is one shipped extension: its import path plus its package.json-style
// self-description (ExtensionInfo flattened — id/name/version/description, repo,
// license, tags, engines, …).
type extension struct {
	Import             string `json:"import"`
	wago.ExtensionInfo        // flattened
}

func main() {
	m := manifest{
		Schema: "wago-plugin/v1",
		Module: "github.com/wago-org/wasi",
		Extensions: []extension{
			{Import: "github.com/wago-org/wasi/p1", ExtensionInfo: p1.Ext(p1.Config{}).Info()},
			{Import: "github.com/wago-org/wasi/unstable", ExtensionInfo: unstable.Ext(unstable.Config{}).Info()},
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep version constraints like ">=0.1.0" literal
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("wago-plugin.json", buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote wago-plugin.json (%d extensions)", len(m.Extensions))
}
