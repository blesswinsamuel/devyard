package engine

import (
	"maps"
	"path/filepath"
	"time"

	"github.com/blesswinsamuel/devyard/internal/config"
)

// inputSums hashes the files a project's config is built from that are known
// without having loaded it (used while the config does not load).
func (a *projectActor) inputSums() config.Sums {
	dir := filepath.Dir(a.reg.ConfigPath)
	sums := config.Sums{}
	for _, p := range []string{
		a.reg.ConfigPath,
		filepath.Join(dir, config.LocalFileName),
		filepath.Join(dir, ".env"),
		filepath.Join(dir, ".env.local"),
	} {
		sums[p] = config.SumFile(p)
	}
	return sums
}

// checkDrift notices changes to the config files and acts on them as the
// reload policy says. It runs on every config check. A change must hold still
// for two checks before it is looked at, so a save made in several steps is
// not caught half way.
func (a *projectActor) checkDrift() {
	if a.ld == nil {
		// The config never loaded: try again once the files change.
		cur := a.inputSums()
		switch {
		case maps.Equal(cur, a.examined):
		case !maps.Equal(cur, a.lastSeen):
			a.lastSeen = cur
		default:
			a.examined = cur
			_ = a.reload()
		}
		return
	}
	policy := a.p.policy(a.reg.ConfigPath)
	if policy == ReloadOff || len(a.ld.sums.Changed()) == 0 {
		if a.drift.State != DriftNone {
			a.drift, a.lastSeen, a.examined = Drift{}, nil, nil
			a.publishView()
		}
		return
	}
	cur := config.Sums{}
	for path := range a.ld.sums {
		cur[path] = config.SumFile(path)
	}
	switch {
	case maps.Equal(cur, a.examined):
	case !maps.Equal(cur, a.lastSeen):
		a.lastSeen = cur
		return
	default:
		a.examine()
		a.examined = cur
	}
	if a.drift.State == DriftPending && policy == ReloadAuto {
		_ = a.reload()
	}
}

// examine loads the files as they are now, without applying them, and
// records how they differ from the loaded config.
func (a *projectActor) examine() {
	reg := a.reg
	next, err := loadProject(a.dirs, &reg, true)
	since := a.drift.Since
	if since.IsZero() {
		since = time.Now()
	}
	switch {
	case err != nil:
		a.drift = Drift{State: DriftInvalid, Error: err.Error(), Since: since}
	default:
		changes := diffLoaded(a.ld, next)
		if len(changes) == 0 {
			// Only comments or formatting changed: nothing to apply.
			a.ld.sums, a.ld.texts = next.sums, next.texts
			a.drift = Drift{}
		} else {
			a.drift = Drift{State: DriftPending, Changes: changes, Diff: textDiff(a.ld.texts, next.texts), Since: since}
		}
	}
	a.publishView()
}
