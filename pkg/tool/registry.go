package tool

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/YasserCR/galdor/pkg/schema"
)

// Registry is a name-indexed collection of tools. Safe for concurrent
// use: Add and lookups are guarded by an RWMutex.
//
// Adapters call ToolDefs() to obtain provider-side definitions for the
// outgoing Request; the executor uses Get() to dispatch incoming tool
// calls back to the matching tool.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]AnyTool
}

// NewRegistry returns a Registry preloaded with the given tools. A
// duplicate name returns an error.
func NewRegistry(tools ...AnyTool) (*Registry, error) {
	r := &Registry{tools: make(map[string]AnyTool, len(tools))}
	for _, t := range tools {
		if err := r.Add(t); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Add registers t. Returns an error if a tool with the same Name() is
// already present.
func (r *Registry) Add(t AnyTool) error {
	if t == nil {
		return fmt.Errorf("registry: nil tool")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("registry: tool has empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; ok {
		return fmt.Errorf("registry: duplicate tool name %q", name)
	}
	r.tools[name] = t
	return nil
}

// Get returns the tool with the given name, if any.
func (r *Registry) Get(name string) (AnyTool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Tools returns the registered tools in stable, name-sorted order.
// Useful for snapshot-style iteration; the returned slice does not
// share storage with the registry's internal map.
func (r *Registry) Tools() []AnyTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AnyTool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

// Len returns the number of registered tools.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// ToolDefs converts the registry into the schema.ToolDef slice that
// provider.Request.Tools expects. The order matches Tools().
func (r *Registry) ToolDefs() ([]schema.ToolDef, error) {
	tools := r.Tools()
	defs := make([]schema.ToolDef, 0, len(tools))
	for _, t := range tools {
		raw, err := json.Marshal(t.Schema())
		if err != nil {
			return nil, fmt.Errorf("registry: marshal schema for %q: %w", t.Name(), err)
		}
		defs = append(defs, schema.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      raw,
		})
	}
	return defs, nil
}
