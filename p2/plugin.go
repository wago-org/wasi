package p2

import (
	"context"
	"fmt"

	"github.com/wago-org/wago/plugin"
	"github.com/wago-org/wago/src/component"
	"github.com/wago-org/wago/src/wago"
	wasi "github.com/wago-org/wasi"
)

// ID is the stable WASI Preview 2 plugin identifier.
const ID = "github.com/wago-org/wasi/p2"

// Extension supplies the WASI Preview 2 world through the Component Model
// plugin's versioned service. It has no direct core-runtime authority.
type Extension struct {
	config     Config
	components *plugin.Ref[component.Service]
	runtime    *Runtime
}

// NewExtension returns an unregistered WASI Preview 2 extension. Mutable slice
// fields are copied so callers cannot change guest arguments or environment
// after granting them to the plugin.
func NewExtension(cfg Config) *Extension {
	cfg.Args = append([]string(nil), cfg.Args...)
	cfg.Env = append([]string(nil), cfg.Env...)
	return &Extension{config: cfg}
}

func (*Extension) Info() wago.ExtensionInfo { return wasi.Info(ID) }

func (e *Extension) Register(reg *wago.Registry) error {
	if e == nil {
		return fmt.Errorf("wasi p2: register nil extension")
	}
	components, err := plugin.Require(reg, component.RuntimeService)
	if err != nil {
		return err
	}
	e.components = components
	e.runtime = &Runtime{components: components, config: e.config}
	return nil
}

// Runtime is a runtime-scoped WASI Preview 2 execution service.
type Runtime struct {
	components *plugin.Ref[component.Service]
	config     Config
}

// Instantiate creates a WASI Preview 2 component instance using only the host
// capabilities explicitly present in the configuration passed to Enable or
// NewExtension.
func (r *Runtime) Instantiate(ctx context.Context, componentBytes []byte) (*component.Instance, error) {
	if r == nil || r.components == nil {
		return nil, fmt.Errorf("wasi p2: nil or inactive runtime")
	}
	components, err := r.components.Get()
	if err != nil {
		return nil, fmt.Errorf("wasi p2: component-model service: %w", err)
	}
	return components.Instantiate(ctx, componentBytes, With(r.config)...)
}

// Enable installs the Component Model provider and the WASI Preview 2 consumer
// into rt. The component plugin alone receives the gated core-engine authority.
func Enable(rt *wago.Runtime, cfg Config) (*Runtime, error) {
	if rt == nil {
		return nil, fmt.Errorf("wasi p2: enable on nil Wago runtime")
	}
	if _, ok := rt.Extension(component.PluginID); !ok {
		if _, err := component.Enable(rt); err != nil {
			return nil, err
		}
	}
	ext := NewExtension(cfg)
	if err := rt.Use(ext); err != nil {
		return nil, err
	}
	return ext.runtime, nil
}
