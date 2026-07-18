package app

import (
	"context"
	"os"
	"time"

	"mihatch/internal/state"
)

// StatusOwnership is the ownership slice of a status report.
type StatusOwnership struct {
	Owned    bool     `json:"owned"`
	Drifted  bool     `json:"drifted"`
	Services []string `json:"services,omitempty"`
}

// StatusReport is the structured result of "mihatch status". It omits all
// secrets and proxy node data; only liveness and ownership flags are exposed.
type StatusReport struct {
	State         state.State     `json:"state"`
	Initialized   bool            `json:"initialized"`
	EngineVersion string          `json:"engine_version,omitempty"`
	MixedPort     int             `json:"mixed_port,omitempty"`
	PID           int             `json:"pid,omitempty"`
	PortListening bool            `json:"port_listening"`
	ProxyOK       bool            `json:"proxy_ok"`
	Ownership     StatusOwnership `json:"ownership"`
}

// Status computes the current lifecycle state from live signals. Read-only: it
// never modifies the system proxy or the process.
func (a *App) Status(ctx context.Context) (StatusReport, error) {
	f, st := a.facts(ctx)
	rep := StatusReport{
		State:         state.Determine(f),
		Initialized:   st.Initialized,
		EngineVersion: st.CoreVersion,
		MixedPort:     a.port(st),
		PortListening: f.PortListening,
		ProxyOK:       f.ProxyOK,
		Ownership: StatusOwnership{
			Owned:    f.OwnershipOwned,
			Drifted:  f.OwnershipDrifted,
			Services: st.Ownership.Services,
		},
	}
	if st.Process != nil {
		rep.PID = st.Process.PID
	}
	return rep, nil
}

// facts gathers live signals for state determination.
func (a *App) facts(ctx context.Context) (state.Facts, *state.Persisted) {
	st, _ := a.loadState()
	f := state.Facts{Initialized: st.Initialized}
	if _, err := os.Stat(a.Paths.Binary()); err == nil {
		f.BinaryExists = true
	}
	f.ProcessRunning = a.processAlive(ctx, st)
	if st.Initialized && f.BinaryExists {
		port := a.port(st)
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		f.PortListening = a.Health.PortListening(probeCtx, port)
		if f.PortListening {
			f.ProxyOK = a.Health.ProxyOK(probeCtx, port, nil, 8*time.Second).OK
		}
		cancel()
	}
	if st.Ownership.Owned {
		f.OwnershipOwned = true
		cur, err := a.acquirer().CurrentFingerprint(ctx, st.Ownership.Services)
		if err != nil {
			f.OwnershipDrifted = true
		} else {
			f.OwnershipDrifted = cur != st.Ownership.AppliedFingerprint
		}
	}
	return f, st
}
