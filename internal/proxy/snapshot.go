package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

// Transaction captures one ownership lifecycle: the settings seen before
// takeover (Before), the settings MiHatch wrote and re-read (Applied), and the
// services involved. It is the unit of compare-before-restore.
type Transaction struct {
	ID                 string         `json:"id"`
	Services           []string       `json:"services"`
	Before             []ServiceProxy `json:"before"`
	Applied            []ServiceProxy `json:"applied"`
	AppliedFingerprint string         `json:"applied_fingerprint,omitempty"`
}

// NewTransactionID returns a fresh opaque transaction id.
func NewTransactionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "tx-fallback"
	}
	return "tx-" + hex.EncodeToString(b[:])
}

// Fingerprint returns a stable, short digest over a snapshot list, used for
// drift detection and terse status. Input is canonicalized (sorted by service)
// so reordering does not change the digest.
func Fingerprint(snapshots []ServiceProxy) string {
	cp := make([]ServiceProxy, len(snapshots))
	copy(cp, snapshots)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Service < cp[j].Service })
	raw, err := json.Marshal(cp)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

// RestoreOutcome describes the per-service result of compare-before-restore.
type RestoreOutcome struct {
	Restored  []string `json:"restored"`  // current==applied: rolled back to Before
	Abandoned []string `json:"abandoned"` // current!=applied: left untouched (drift)
}

// Drifted reports whether any service was abandoned.
func (r RestoreOutcome) Drifted() bool { return len(r.Abandoned) > 0 }

// PlanRestore implements compare-before-restore purely:
//   - For each service in Applied, compare Current to what MiHatch wrote
//     (Applied).
//   - Match  -> safe to restore Before.
//   - Differ -> a third party changed it; abandon (do NOT overwrite).
func PlanRestore(before, applied, current []ServiceProxy) (RestoreOutcome, error) {
	out := RestoreOutcome{}
	if len(applied) == 0 {
		return out, errors.New("plan restore: no applied snapshot")
	}
	for i := range applied {
		svc := applied[i].Service
		cur, ok := Find(current, svc)
		if !ok {
			out.Abandoned = append(out.Abandoned, svc)
			continue
		}
		if !applied[i].Equal(cur) {
			out.Abandoned = append(out.Abandoned, svc)
			continue
		}
		if _, ok := Find(before, svc); !ok {
			out.Abandoned = append(out.Abandoned, svc)
			continue
		}
		out.Restored = append(out.Restored, svc)
	}
	return out, nil
}

// DesiredFromTransaction returns the Applied settings (what MiHatch wrote).
func (t Transaction) Desired() []ServiceProxy { return t.Applied }
