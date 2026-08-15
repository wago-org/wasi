package core

import (
	wago "github.com/wago-org/wago"
)

// Definition constructs the common immutable metadata for one versioned WASI
// provider. Each authority is exact and non-inheriting.
func Definition(id, name, description string, stability wago.Stability, module string) wago.PluginDefinition {
	return wago.PluginDefinition{
		ID:          id,
		Name:        name,
		Version:     "0.1.1",
		Description: description,
		Stability:   stability,
		Compatibility: wago.Compatibility{
			Engines:   map[string]string{"wago": ">=0.1.0", "go": ">=1.22"},
			Platforms: []string{"darwin/amd64", "darwin/arm64", "linux/amd64"},
		},
		Provenance: wago.PluginProvenance{
			Homepage:   "https://github.com/wago-org/wasi",
			Repository: "https://github.com/wago-org/wasi",
			License:    "Apache-2.0",
			Authors:    []string{"The Wago authors"},
		},
		Authorities: []wago.AuthorityRequest{
			{
				Name:   wago.AuthorityHostImportDefine,
				Mode:   wago.AuthorityRequired,
				Reason: "define the selected WASI snapshot's host functions",
				Scope:  wago.AuthorityScope{Modules: []string{module}},
			},
			{
				Name:   wago.AuthorityHostCallerIdentify,
				Mode:   wago.AuthorityRequired,
				Reason: "isolate descriptor tables by the opaque identity of the active guest",
			},
			{
				Name:   wago.AuthorityHostArgumentsRead,
				Mode:   wago.AuthorityRequired,
				Reason: "expose this runtime's immutable guest argv through Preview 1",
			},
			{
				Name:   wago.AuthorityInstanceCloseObserve,
				Mode:   wago.AuthorityRequired,
				Reason: "close the departed guest's files without receiving instance control",
			},
		},
		ConfigSchema: ConfigSchema(),
	}
}
