package module

import (
	"sync"

	"github.com/gin-gonic/gin"
)

type Route struct {
	Method  string
	Path    string
	Handler gin.HandlerFunc
}

type ModelRegistration struct {
	Manifest ModelDef
	TableName string
	SQL       string
}

type ModuleInstance struct {
	Manifest *Manifest
	Routes   []Route
	Models   []ModelRegistration
	OnLoad   func() error
	OnUnload func() error
}

type MenuItem struct {
	Module  string `json:"module"`
	Label   string `json:"label"`
	Icon    string `json:"icon"`
	Route   string `json:"route"`
	Seq     int    `json:"seq"`
}

type Registry struct {
	mu      sync.RWMutex
	modules map[string]*ModuleInstance
}

var Global = &Registry{
	modules: make(map[string]*ModuleInstance),
}

func (r *Registry) Register(mod *ModuleInstance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.modules[mod.Manifest.Name] = mod
}

func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.modules, name)
}

func (r *Registry) Get(name string) *ModuleInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.modules[name]
}

func (r *Registry) All() []*ModuleInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mods := make([]*ModuleInstance, 0, len(r.modules))
	for _, m := range r.modules {
		mods = append(mods, m)
	}
	return mods
}

func (r *Registry) Menus() []MenuItem {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var items []MenuItem
	for _, m := range r.modules {
		if m.Manifest == nil {
			continue
		}
		for _, menu := range m.Manifest.Menus {
			items = append(items, MenuItem{
				Module: m.Manifest.Name,
				Label:  menu.Label,
				Icon:   menu.Icon,
				Route:  menu.Route,
				Seq:    menu.Seq,
			})
		}
	}
	return items
}
