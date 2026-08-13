package wasi_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/wago"
	"github.com/wago-org/wasi"
	"github.com/wago-org/wasi/p1"
	"github.com/wago-org/wasi/p2"
	wasiregister "github.com/wago-org/wasi/register"
	"github.com/wago-org/wasi/unstable"
)

type manifestAuthor struct {
	Name string `json:"name"`
}

type manifestPackage struct {
	Module      string            `json:"module"`
	Version     string            `json:"version"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Stability   wago.Stability    `json:"stability"`
	License     string            `json:"license"`
	Homepage    string            `json:"homepage"`
	Repository  string            `json:"repository"`
	Authors     []manifestAuthor  `json:"authors"`
	Engines     map[string]string `json:"engines"`
	Platforms   []string          `json:"platforms"`
	Subpackages []manifestPackage `json:"subpackages"`
}

func TestManifestMatchesEveryCatalogDefinition(t *testing.T) {
	raw, err := os.ReadFile("wago.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Schema  string            `json:"$schema"`
		Package manifestPackage   `json:"package"`
		Plugins map[string]string `json:"plugins"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != "https://wago.sh/v1/schema.json" {
		t.Fatalf("manifest schema = %q", manifest.Schema)
	}
	if !reflect.DeepEqual(manifest.Plugins, map[string]string{component.PluginID: "^0.1.0"}) {
		t.Fatalf("manifest dependencies = %v", manifest.Plugins)
	}

	canonical := map[string]wago.PluginDefinition{
		wasi.ID:     wasi.Definition(),
		p1.ID:       p1.Definition(),
		p2.ID:       p2.Definition(),
		unstable.ID: unstable.Definition(),
	}
	providers := wasiregister.Providers()
	if len(providers) != len(canonical) {
		t.Fatalf("catalog providers = %d, want %d", len(providers), len(canonical))
	}
	for _, provider := range providers {
		definition, ok := canonical[provider.Definition.ID]
		if !ok {
			t.Fatalf("unexpected catalog provider %q", provider.Definition.ID)
		}
		if !reflect.DeepEqual(provider.Definition, definition) {
			t.Fatalf("%s catalog definition drifted\ncatalog=%#v\ncanonical=%#v", provider.Definition.ID, provider.Definition, definition)
		}
	}

	metadata := map[string]manifestPackage{manifest.Package.Module: manifest.Package}
	for _, subpackage := range manifest.Package.Subpackages {
		metadata[subpackage.Module] = inheritManifestMetadata(manifest.Package, subpackage)
	}
	if len(metadata) != len(canonical) {
		t.Fatalf("manifest entries = %d, want %d", len(metadata), len(canonical))
	}
	for id, definition := range canonical {
		entry, ok := metadata[id]
		if !ok {
			t.Fatalf("manifest omits catalog provider %q", id)
		}
		assertManifestMetadata(t, entry, definition)
	}
	assertProviderCatalogCurrent(t, "github.com/wago-org/wasi/register", providers)
}

func assertProviderCatalogCurrent(t *testing.T, importPath string, providers []wago.PluginProvider) {
	t.Helper()
	want, err := wago.EncodeProviderCatalog(importPath, providers)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(wago.ProviderCatalogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s is stale; run wago plugin catalog", wago.ProviderCatalogFile)
	}
	if _, err := wago.DecodeProviderCatalog(got); err != nil {
		t.Fatalf("%s: %v", wago.ProviderCatalogFile, err)
	}
}

func inheritManifestMetadata(root, subpackage manifestPackage) manifestPackage {
	if subpackage.Version == "" {
		subpackage.Version = root.Version
	}
	if subpackage.License == "" {
		subpackage.License = root.License
	}
	if subpackage.Homepage == "" {
		subpackage.Homepage = root.Homepage
	}
	if subpackage.Repository == "" {
		subpackage.Repository = root.Repository
	}
	if subpackage.Authors == nil {
		subpackage.Authors = root.Authors
	}
	if subpackage.Engines == nil {
		subpackage.Engines = root.Engines
	}
	if subpackage.Platforms == nil {
		subpackage.Platforms = root.Platforms
	}
	return subpackage
}

func assertManifestMetadata(t *testing.T, manifest manifestPackage, definition wago.PluginDefinition) {
	t.Helper()
	authors := make([]string, len(manifest.Authors))
	for i := range manifest.Authors {
		authors[i] = manifest.Authors[i].Name
	}
	if manifest.Module != definition.ID ||
		manifest.Version != definition.Version ||
		manifest.Name != definition.Name ||
		manifest.Description != definition.Description ||
		manifest.Stability != definition.Stability ||
		manifest.License != definition.Provenance.License ||
		manifest.Homepage != definition.Provenance.Homepage ||
		manifest.Repository != definition.Provenance.Repository ||
		!reflect.DeepEqual(authors, definition.Provenance.Authors) ||
		!reflect.DeepEqual(manifest.Engines, definition.Compatibility.Engines) ||
		!reflect.DeepEqual(manifest.Platforms, definition.Compatibility.Platforms) {
		t.Fatalf("%s manifest metadata drifted\nmanifest=%#v\ndefinition=%#v", definition.ID, manifest, definition)
	}
}
