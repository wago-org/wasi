// Command genmanifest regenerates wago-plugin.json from the extensions this module
// provides. It registers each extension on a throwaway wago.Runtime and records
// its self-description (ExtensionInfo), declared capabilities, and host imports —
// the ExtensionManifest a registry or build tool can read without compiling.
//
// Run from the module root:
//
//	go run ./internal/genmanifest
package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"sort"

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

type extension struct {
	Import             string    `json:"import"` // Go import path of the constructor package
	wago.ExtensionInfo           // flattened identity/provenance/compatibility
	Capabilities       []string  `json:"capabilities,omitempty"`
	Imports            []himport `json:"imports,omitempty"`
}

type himport struct {
	Module     string   `json:"module"`
	Name       string   `json:"name"`
	Params     []string `json:"params,omitempty"`
	Results    []string `json:"results,omitempty"`
	Capability string   `json:"capability,omitempty"`
	Docs       string   `json:"docs,omitempty"`
}

func describe(importPath string, ext wago.Extension) extension {
	e := extension{Import: importPath, ExtensionInfo: ext.Info()}
	rt := wago.NewRuntime()
	if err := rt.Use(ext); err != nil {
		log.Fatalf("use %s: %v", ext.Info().ID, err)
	}
	for _, c := range rt.Capabilities() {
		e.Capabilities = append(e.Capabilities, string(c))
	}
	for _, s := range rt.ProvidedImports() {
		e.Imports = append(e.Imports, himport{
			Module:     s.Module,
			Name:       s.Name,
			Params:     valTypes(s.Params),
			Results:    valTypes(s.Results),
			Capability: capOf(s),
			Docs:       s.Docs,
		})
	}
	sort.Slice(e.Imports, func(i, j int) bool {
		return e.Imports[i].Module+"."+e.Imports[i].Name < e.Imports[j].Module+"."+e.Imports[j].Name
	})
	return e
}

func valTypes(ts []wago.ValType) []string {
	if len(ts) == 0 {
		return nil
	}
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.String()
	}
	return out
}

func capOf(s wago.ImportSpec) string {
	if s.HasCapability {
		return string(s.Capability)
	}
	return ""
}

func main() {
	m := manifest{
		Schema: "wago-plugin/v1",
		Module: "github.com/wago-org/wasi",
		Extensions: []extension{
			describe("github.com/wago-org/wasi/p1", p1.Ext(p1.Config{})),
			describe("github.com/wago-org/wasi/unstable", unstable.Ext(unstable.Config{})),
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
