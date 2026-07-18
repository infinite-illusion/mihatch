package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"mihatch/internal/exit"
	"mihatch/internal/paths"
	"mihatch/internal/proxy"
	"mihatch/internal/state"
)

// UpOpts configures up/resume.
type UpOpts struct {
	Services  []string
	ForceAuth bool
}

// Up starts Mihomo (if not already healthy), waits for the mixed proxy to
// serve, then takes over the system proxy. Idempotent: a healthy, owned MiHatch
// is a no-op success.
func (a *App) Up(ctx context.Context, opts UpOpts) error {
	release, err := a.acquireLock(ctx)
	if err != nil {
		return exit.New(exit.CodeLocked, err)
	}
	defer release()

	st, err := a.loadState()
	if err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	if !st.Initialized {
		return exit.New(exit.CodeUninitialized, errors.New("not initialized; run 'mihatch init' first"))
	}
	if _, err := os.Stat(a.Paths.Binary()); err != nil {
		return exit.New(exit.CodeUninitialized, errors.New("engine missing; run 'mihatch init'"))
	}
	if _, err := os.Stat(a.Paths.ConfigFile()); err != nil {
		return exit.New(exit.CodeConfig, errors.New("config missing; run 'mihatch init'"))
	}
	if err := a.Mihomo.Validate(ctx, a.Paths.Binary(), a.Paths.DotDir(), a.Paths.ConfigFile()); err != nil {
		return exit.New(exit.CodeConfig, err)
	}

	port := a.port(st)
	alive := a.processAlive(ctx, st)

	if alive {
		healthy := a.waitHealthy(ctx, port, 12*time.Second)
		if !healthy {
			// Self-heal: stop the broken process and start fresh below.
			a.stopProcess(ctx, st)
			st.Process = nil
			alive = false
		}
	}

	if alive {
		// Already running and healthy.
		if st.Ownership.Owned {
			drifted, _ := a.checkDrift(ctx, st.Ownership)
			if drifted {
				st.Ownership = state.Ownership{}
				st.LastState = state.StateDegraded
				_ = a.saveState(st)
				return exit.New(exit.CodeDrifted, errors.New("system proxy changed by another app; ownership released for safety"))
			}
			fmt.Fprintln(a.Out, "MiHatch is already up and owns the system proxy.")
			return nil
		}
		services, err := a.resolveServices(ctx, opts.Services)
		if err != nil {
			return exit.New(exit.CodeConfig, err)
		}
		if err := a.takeOwnership(ctx, st, services, opts.ForceAuth); err != nil {
			return err
		}
		fmt.Fprintln(a.Out, "MiHatch resumed system-proxy ownership.")
		return nil
	}

	// Not running (or just stopped): start fresh.
	fmt.Fprintln(a.Err, "Starting Mihomo and waiting for the proxy...")
	handle, err := a.Mihomo.Start(a.Paths.Binary(), a.Paths.DotDir(), a.Paths.ConfigFile(), a.Paths.LogFile())
	if err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	st.Process = &state.ProcessInfo{
		PID:        handle.PID,
		StartTime:  handle.StartTime,
		BinaryPath: handle.Binary,
		StartedAt:  a.now(),
	}
	if !a.waitHealthy(ctx, port, 30*time.Second) {
		st.LastState = state.StateDegraded
		_ = a.saveState(st)
		return exit.New(exit.CodeUnhealthy, errors.New("mihomo started but the health check failed; inspect 'mihatch logs', then 'mihatch down' and retry 'mihatch up'"))
	}
	services, err := a.resolveServices(ctx, opts.Services)
	if err != nil {
		st.MarkUp(a.now(), st.Process, false)
		_ = a.saveState(st)
		return exit.New(exit.CodeConfig, err)
	}
	if err := a.takeOwnership(ctx, st, services, opts.ForceAuth); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "MiHatch is up. System proxy -> 127.0.0.1:%d on %s.\n", port, strings.Join(services, ", "))
	fmt.Fprintln(a.Out, "CLI/apps that ignore the macOS system proxy (codex, git, node, …) also need:")
	fmt.Fprintf(a.Out, "  export HTTPS_PROXY=http://127.0.0.1:%d HTTP_PROXY=http://127.0.0.1:%d\n", port, port)
	return nil
}

// takeOwnership acquires the system proxy and records the transaction. It does
// not print (callers message). On authenticated-proxy refusal it leaves MiHatch
// in standby.
func (a *App) takeOwnership(ctx context.Context, st *state.Persisted, services []string, forceAuth bool) error {
	acq := a.acquirer()
	tx, err := acq.Acquire(ctx, services, forceAuth)
	if err != nil {
		st.MarkUp(a.now(), st.Process, false)
		_ = a.saveState(st)
		if errors.Is(err, proxy.ErrAuthenticatedProxy) {
			return exit.New(exit.CodeConfig, err)
		}
		return exit.New(exit.CodeGeneral, fmt.Errorf("take system proxy: %w", err))
	}
	st.MarkUp(a.now(), st.Process, true)
	st.SetOwnership(state.Ownership{
		Owned:              true,
		TransactionID:      tx.ID,
		AppliedFingerprint: tx.AppliedFingerprint,
		Services:           services,
		AcquiredAt:         a.now(),
		Before:             tx.Before,
		Applied:            tx.Applied,
	})
	if err := a.saveState(st); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	return nil
}

// Pause releases the system proxy (compare-before-restore) but keeps Mihomo
// running, so Clash Verge Rev dev can test system proxy / TUN.
func (a *App) Pause(ctx context.Context) error {
	release, err := a.acquireLock(ctx)
	if err != nil {
		return exit.New(exit.CodeLocked, err)
	}
	defer release()

	st, err := a.loadState()
	if err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	if !st.Initialized {
		return exit.New(exit.CodeUninitialized, errors.New("not initialized"))
	}
	if !st.Ownership.Owned {
		fmt.Fprintln(a.Out, "MiHatch does not own the system proxy; nothing to pause.")
		return nil
	}
	outcome, err := a.restoreOwnership(ctx, st.Ownership)
	if err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	st.ClearOwnership(a.now())
	if err := a.saveState(st); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	if outcome.Drifted() {
		fmt.Fprintf(a.Err, "Paused with a warning: system proxy had been changed by another app on %s; left untouched.\n", strings.Join(outcome.Abandoned, ", "))
		return exit.New(exit.CodeDrifted, errors.New("proxy drifted; not restored"))
	}
	fmt.Fprintln(a.Out, "Paused: system proxy released. Mihomo still running.")
	return nil
}

// Resume re-acquires the system proxy with a fresh before-snapshot after
// confirming Mihomo is healthy.
func (a *App) Resume(ctx context.Context, opts UpOpts) error {
	release, err := a.acquireLock(ctx)
	if err != nil {
		return exit.New(exit.CodeLocked, err)
	}
	defer release()

	st, err := a.loadState()
	if err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	if !st.Initialized {
		return exit.New(exit.CodeUninitialized, errors.New("not initialized"))
	}
	port := a.port(st)
	healthy := a.processAlive(ctx, st) && a.Health.PortListening(ctx, port) && a.Health.ProxyOK(ctx, port, nil, 8*time.Second).OK
	if !healthy {
		return exit.New(exit.CodeUnhealthy, errors.New("mihomo is not healthy; run 'mihatch up' instead"))
	}
	services, err := a.resolveServices(ctx, opts.Services)
	if err != nil {
		return exit.New(exit.CodeConfig, err)
	}
	if err := a.takeOwnership(ctx, st, services, opts.ForceAuth); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "Resumed system-proxy ownership.")
	return nil
}

// Down restores the system proxy (if still owned) and stops Mihomo.
func (a *App) Down(ctx context.Context) error {
	release, err := a.acquireLock(ctx)
	if err != nil {
		return exit.New(exit.CodeLocked, err)
	}
	defer release()

	st, err := a.loadState()
	if err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	if !st.Initialized {
		fmt.Fprintln(a.Out, "MiHatch is not initialized; nothing to bring down.")
		return nil
	}

	drifted := false
	if st.Ownership.Owned {
		outcome, rerr := a.restoreOwnership(ctx, st.Ownership)
		if rerr == nil && outcome.Drifted() {
			drifted = true
		}
	}
	a.stopProcess(ctx, st)
	st.MarkDown(a.now())
	if err := a.saveState(st); err != nil {
		return exit.New(exit.CodeGeneral, err)
	}
	if drifted {
		fmt.Fprintln(a.Err, "Down: system proxy had drifted (changed by another app) and was left untouched. Mihomo stopped.")
		return exit.New(exit.CodeDrifted, errors.New("proxy drifted; not restored"))
	}
	fmt.Fprintln(a.Out, "MiHatch is down.")
	return nil
}

// restoreOwnership runs compare-before-restore for an ownership record.
func (a *App) restoreOwnership(ctx context.Context, own state.Ownership) (proxy.RestoreOutcome, error) {
	acq := a.acquirer()
	return acq.Restore(ctx, proxy.Transaction{
		Services: own.Services,
		Before:   own.Before,
		Applied:  own.Applied,
	})
}

func (a *App) checkDrift(ctx context.Context, own state.Ownership) (bool, error) {
	acq := a.acquirer()
	cur, err := acq.CurrentFingerprint(ctx, own.Services)
	if err != nil {
		return true, err
	}
	return cur != own.AppliedFingerprint, nil
}

func (a *App) processAlive(ctx context.Context, st *state.Persisted) bool {
	if st.Process == nil {
		return false
	}
	alive, _ := a.Mihomo.IsAlive(ctx, st.Process.PID, st.Process.StartTime, st.Process.BinaryPath)
	return alive
}

func (a *App) stopProcess(ctx context.Context, st *state.Persisted) {
	if st.Process == nil {
		return
	}
	_ = a.Mihomo.Stop(ctx, st.Process.PID, st.Process.StartTime, st.Process.BinaryPath, 8*time.Second)
}

func (a *App) port(st *state.Persisted) int {
	if st.MixedPort > 0 {
		return st.MixedPort
	}
	return paths.DefaultMixedPort
}
