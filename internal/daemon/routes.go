//go:build unix

package daemon

import (
	"github.com/blesswinsamuel/devyard/internal/engine"
	"github.com/blesswinsamuel/devyard/internal/events"
	"github.com/blesswinsamuel/devyard/internal/proxy"
)

// routes resolves proxy routes from the current project views and the live
// service status on the bus.
type routes struct {
	mgr *engine.Manager
	bus *events.Bus
}

func (r routes) ProxyRoutes() []proxy.Route {
	var out []proxy.Route
	for _, p := range r.mgr.List() {
		v := p.View()
		for _, name := range v.Order {
			def := v.ServiceDefs[name]
			if def == nil || len(def.Ports) == 0 {
				continue
			}
			status := ""
			if svc := r.bus.Service(def.Project, name); svc != nil {
				status = svc.Status
			}
			for i, port := range def.Ports {
				out = append(out, proxy.Route{
					Label:     def.ProxyHost,
					IsDefault: i == 0 && v.Primary == name,
					Project:   def.Project,
					Service:   name,
					PortName:  port.Name,
					Port:      port.Port,
					Status:    status,
				})
			}
		}
	}
	return out
}
