package module

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/labmk/obs-viewer/internal/config"
)

// Registry collects modules and mounts the enabled ones at boot.
//
// Add() order is preserved in the /api/modules response, so the SPA can
// use it for stable tab ordering.
type Registry struct {
	cfg     *config.Config
	deps    Deps
	modules []Module
	// Filled by Boot; only enabled modules' manifests, in Add() order.
	manifests []*Manifest
	booted    bool
}

// NewRegistry creates a registry bound to a config and a Deps payload.
// Deps fields may be nil — modules that need a particular field should
// fail fast in Register.
func NewRegistry(cfg *config.Config, deps Deps) *Registry {
	return &Registry{
		cfg:       cfg,
		deps:      deps,
		manifests: []*Manifest{}, // never nil, so /api/modules returns []
	}
}

// Add registers a module. Must be called before Boot.
func (r *Registry) Add(m Module) {
	if r.booted {
		log.Printf("module %q: Add() after Boot() — ignored", m.Name())
		return
	}
	r.modules = append(r.modules, m)
}

// Boot mounts enabled modules on mux and exposes /api/modules.
// Returns the first Register error; modules registered before the
// failure remain mounted.
func (r *Registry) Boot(mux *http.ServeMux) error {
	if r.booted {
		return fmt.Errorf("module registry: already booted")
	}
	r.booted = true

	for _, m := range r.modules {
		name := m.Name()
		if !m.Enabled(r.cfg) {
			log.Printf("module %q: disabled", name)
			continue
		}
		mf := &Manifest{ID: name}
		ctx := &RegisterContext{
			Deps:     r.deps,
			Mux:      mux,
			Manifest: mf,
			Config:   r.cfg,
		}
		if err := m.Register(ctx); err != nil {
			return fmt.Errorf("module %q register: %w", name, err)
		}
		if mf.ID == "" {
			mf.ID = name
		}
		r.manifests = append(r.manifests, mf)
		log.Printf("module %q: enabled", name)
	}

	mux.HandleFunc("/api/modules", r.handleListModules)
	return nil
}

// Manifests returns a copy of the enabled-module manifests, in Add()
// order. Useful for tests; production code should hit /api/modules.
func (r *Registry) Manifests() []*Manifest {
	out := make([]*Manifest, len(r.manifests))
	copy(out, r.manifests)
	return out
}

// @Summary      List enabled modules
// @Description  Returns one manifest per enabled module in Add() order. The SPA reads this at boot to decide which tabs to render and to inject the branding stylesheet + favicons.
// @Tags         modules
// @Produce      json
// @Success      200  {object}  server.ModulesResponse
// @Router       /api/modules [get]
func (r *Registry) handleListModules(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"modules": r.manifests,
	})
}
