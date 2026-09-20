package module

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/choigonyok/jarvis/agent/internal/core/action"
)

// Registry holds the enabled modules and sorts them by capability. Adding a
// feature is one Add call in main; removing one is deleting that line.
type Registry struct {
	mu        sync.RWMutex
	all       []Module
	sources   []Source
	actuators map[string]Actuator
	contexts  []ContextSource
	specs     map[string]action.Spec
}

func NewRegistry() *Registry {
	return &Registry{
		actuators: map[string]Actuator{},
		specs:     map[string]action.Spec{},
	}
}

// Add classifies by type assertion: a module is registered for each of the
// three roles it happens to implement, and for none of the others.
func (r *Registry) Add(m Module) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.all = append(r.all, m)
	if s, ok := m.(Source); ok {
		r.sources = append(r.sources, s)
	}
	if c, ok := m.(ContextSource); ok {
		r.contexts = append(r.contexts, c)
	}
	if a, ok := m.(Actuator); ok {
		r.actuators[m.Name()] = a
		for _, spec := range a.Specs() {
			r.specs[spec.Kind] = spec
		}
	}
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.all))
	for _, m := range r.all {
		out = append(out, m.Name())
	}
	return out
}

// Lookup resolves an action kind to the module that runs it and the spec it
// was declared with. An unknown kind is not an error worth a type: the caller
// decides whether that is a rejection or a generic card.
func (r *Registry) Lookup(kind string) (Actuator, action.Spec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[kind]
	if !ok {
		return nil, action.Spec{}, false
	}
	name, _, _ := strings.Cut(kind, ".")
	a, ok := r.actuators[name]
	if !ok {
		return nil, action.Spec{}, false
	}
	return a, spec, true
}

func (r *Registry) Sources() []Source {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Source(nil), r.sources...)
}

func (r *Registry) ContextSources() []ContextSource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]ContextSource(nil), r.contexts...)
}

func (r *Registry) Start(ctx context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.all {
		if err := m.Start(ctx); err != nil {
			return fmt.Errorf("%s 모듈 시작: %w", m.Name(), err)
		}
	}
	return nil
}

// Stop runs every module's Stop even if one fails: a module that cannot shut
// down cleanly must not keep the others open.
func (r *Registry) Stop(ctx context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var first error
	for _, m := range r.all {
		if err := m.Stop(ctx); err != nil && first == nil {
			first = fmt.Errorf("%s 모듈 종료: %w", m.Name(), err)
		}
	}
	return first
}
