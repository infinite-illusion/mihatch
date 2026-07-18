package proxy

import (
	"context"
	"errors"
	"fmt"
)

// ErrAuthenticatedProxy is returned by Acquire when a target service already
// has an authenticated proxy that MiHatch cannot safely restore.
var ErrAuthenticatedProxy = errors.New("target service has an authenticated proxy that MiHatch cannot restore; pass --force to proceed")

// Acquirer drives system-proxy takeover and release on top of a Client.
type Acquirer struct {
	Client      *Client
	Host        string
	Port        int
	Bypass      []string
	EnableHTTP  bool
	EnableHTTPS bool
	EnableSOCKS bool
}

// Snapshot reads the current proxy settings for the given services.
func (a *Acquirer) Snapshot(ctx context.Context, services []string) ([]ServiceProxy, error) {
	out := make([]ServiceProxy, 0, len(services))
	for _, svc := range services {
		got, err := a.Client.Get(ctx, svc)
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, nil
}

// DesiredFor builds the MiHatch target configuration for a service.
func (a *Acquirer) DesiredFor(svc string) ServiceProxy {
	port := PortString(a.Port)
	return ServiceProxy{
		Service:      svc,
		WebProxy:     ProxyEntry{Enabled: a.EnableHTTP, Server: a.Host, Port: port},
		SecureProxy:  ProxyEntry{Enabled: a.EnableHTTPS, Server: a.Host, Port: port},
		SOCKSProxy:   ProxyEntry{Enabled: a.EnableSOCKS, Server: a.Host, Port: port},
		AutoProxy:    AutoProxy{Enabled: false},
		AutoDiscover: false,
		Bypass:       a.Bypass,
	}
}

// desiredAll builds the desired settings for every service.
func (a *Acquirer) desiredAll(services []string) []ServiceProxy {
	out := make([]ServiceProxy, 0, len(services))
	for _, svc := range services {
		out = append(out, a.DesiredFor(svc))
	}
	return out
}

// Acquire takes ownership of the system proxy for the given services:
//  1. snapshot the current settings (Before);
//  2. refuse if any service has an authenticated proxy unless allowAuth;
//  3. apply the desired settings;
//  4. re-read (Applied) and compute the applied fingerprint.
//
// On a mid-acquire failure it attempts a best-effort rollback of any service
// already applied by restoring Before for those services.
func (a *Acquirer) Acquire(ctx context.Context, services []string, allowAuth bool) (Transaction, error) {
	before, err := a.Snapshot(ctx, services)
	if err != nil {
		return Transaction{}, fmt.Errorf("snapshot before: %w", err)
	}
	if !allowAuth {
		for _, s := range before {
			if s.HasAuthenticatedProxy() {
				return Transaction{}, fmt.Errorf("%w (service %q)", ErrAuthenticatedProxy, s.Service)
			}
		}
	}
	tx := Transaction{
		ID:       NewTransactionID(),
		Services: services,
		Before:   before,
	}
	applied := make([]ServiceProxy, 0, len(services))
	for i, svc := range services {
		if err := a.Client.Apply(ctx, a.DesiredFor(svc)); err != nil {
			// Best-effort rollback of services already applied.
			for j := 0; j < i; j++ {
				_ = a.Client.Apply(ctx, before[j])
			}
			return tx, fmt.Errorf("apply proxy for %q: %w", svc, err)
		}
		got, err := a.Client.Get(ctx, svc)
		if err != nil {
			return tx, fmt.Errorf("re-read applied for %q: %w", svc, err)
		}
		applied = append(applied, got)
	}
	tx.Applied = applied
	tx.AppliedFingerprint = Fingerprint(applied)
	return tx, nil
}

// Restore releases ownership using compare-before-restore:
//   - for each service, if the current settings still equal what MiHatch
//     applied, roll back to Before;
//   - if they differ (a third party changed them), abandon and do NOT
//     overwrite.
//
// Services abandoned by drift are reported; they are not restored and MiHatch
// drops ownership of them.
func (a *Acquirer) Restore(ctx context.Context, tx Transaction) (RestoreOutcome, error) {
	current, err := a.Snapshot(ctx, tx.Services)
	if err != nil {
		return RestoreOutcome{}, fmt.Errorf("snapshot current: %w", err)
	}
	outcome, err := PlanRestore(tx.Before, tx.Applied, current)
	if err != nil {
		return RestoreOutcome{}, err
	}
	beforeByName := map[string]ServiceProxy{}
	for _, s := range tx.Before {
		beforeByName[s.Service] = s
	}
	for _, svc := range outcome.Restored {
		if b, ok := beforeByName[svc]; ok {
			if err := a.Client.Apply(ctx, b); err != nil {
				return outcome, fmt.Errorf("restore %q: %w", svc, err)
			}
		}
	}
	return outcome, nil
}

// CurrentFingerprint returns the fingerprint of the live settings for drift
// detection against a transaction's AppliedFingerprint.
func (a *Acquirer) CurrentFingerprint(ctx context.Context, services []string) (string, error) {
	cur, err := a.Snapshot(ctx, services)
	if err != nil {
		return "", err
	}
	return Fingerprint(cur), nil
}
