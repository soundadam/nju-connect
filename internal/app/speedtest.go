package app

import (
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
	"github.com/soundadam/nju-connect/internal/speedtest"
)

// Speedtest is the campus speed test wired to this state directory.
type Speedtest struct {
	// Component installs and locates the measurement helper.
	Component speedtest.ComponentManager
	// Service measures routes and keeps the last result.
	Service speedtest.Service
	// Store holds the last result.
	Store speedtest.Store
}

// OpenSpeedtest wires the speed test to the state directory, the running
// runtime's control socket, and the helper Deps.Speedtest locates.
func OpenSpeedtest(deps Deps) (Speedtest, error) {
	paths, err := deps.Paths()
	if err != nil {
		return Speedtest{}, err
	}
	component := speedtest.ComponentManager{
		Root:         speedtest.ComponentRoot(paths.Root),
		Asset:        deps.Speedtest.Asset(),
		ExternalPath: deps.Speedtest.ExternalPath(),
		Client:       deps.Speedtest.HTTPClient(),
	}
	store := speedtest.Store{Path: speedtest.LastResultPath(paths.Root)}
	return Speedtest{
		Component: component,
		Store:     store,
		Service: speedtest.Service{
			HelperPath: component.ExecutablePath(),
			Store:      store,
			Probe:      deps.Speedtest.Probe,
			RuntimeStatus: func() (speedtest.RuntimeState, error) {
				snapshot, err := runtimecontrol.Query(runtimecontrol.Path(paths.Root))
				if err != nil {
					return speedtest.RuntimeState{}, err
				}
				return speedtest.RuntimeState{Connected: snapshot.State == "connected", SOCKSListen: snapshot.SOCKSListen}, nil
			},
		},
	}, nil
}
