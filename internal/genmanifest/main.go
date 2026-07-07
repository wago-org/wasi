// Command genmanifest regenerates wago-plugin.json from the subpackages this module
// provides. The manifest is package.json-style: shared module metadata at the top
// (name, version, provenance, engines) and a subpackages map for the extensions it
// ships — the metadata a registry or catalog reads without compiling. (Host-import
// signatures are deliberately left out; discover those by compiling or via
// `wago plugin inspect`.)
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

	"github.com/wago-org/wasi/p1"
	"github.com/wago-org/wasi/unstable"
)

// manifest is the wago-plugin.json document: shared module metadata plus the
// subpackages this module ships. Modeled on package.json.
type manifest struct {
	Name         string                `json:"name"` // module path
	Version      string                `json:"version,omitempty"`
	Description  string                `json:"description,omitempty"`
	License      string                `json:"license,omitempty"`
	Homepage     string                `json:"homepage,omitempty"`
	Repository   string                `json:"repository,omitempty"`
	Author       string                `json:"author,omitempty"`
	Contributors []string              `json:"contributors,omitempty"`
	Keywords     []string              `json:"keywords,omitempty"`
	Engines      map[string]string     `json:"engines,omitempty"`
	Subpackages  map[string]subpackage `json:"subpackages,omitempty"`
}

// subpackage is one shipped extension, keyed in the map by its short name (joined
// to the module path for its import). It is either an inline description or a path
// string pointing to another wago-plugin.json that describes it.
type subpackage struct {
	Description string // inline form
	Path        string // path form, e.g. "./extra/wago-plugin.json"
}

func (s subpackage) MarshalJSON() ([]byte, error) {
	if s.Path != "" {
		return json.Marshal(s.Path)
	}
	return json.Marshal(struct {
		Description string `json:"description,omitempty"`
	}{s.Description})
}

func (s *subpackage) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &s.Path)
	}
	var o struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return err
	}
	s.Description = o.Description
	return nil
}

func main() {
	m := manifest{
		Name:         "github.com/wago-org/wasi",
		Version:      "1.0.0",
		Description:  "WASI host functions for wago.",
		License:      "Apache-2.0",
		Homepage:     "https://github.com/wago-org/wasi",
		Repository:   "https://github.com/wago-org/wasi",
		Author:       "The wago authors",
		Keywords:     []string{"wasi", "syscall", "posix", "stdio"},
		Engines:      map[string]string{"wago": ">=0.1.0", "tinygo": "*"},
		Subpackages: map[string]subpackage{
			"p1":       {Description: p1.Ext(p1.Config{}).Info().Description},
			"unstable": {Description: unstable.Ext(unstable.Config{}).Info().Description},
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
	log.Printf("wrote wago-plugin.json (%d subpackages)", len(m.Subpackages))
}
