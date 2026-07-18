// Package state defines MiHatch's logical lifecycle states, the persisted
// state.json, and the pure truth-based state determination.
//
// state.json records intent and ownership provenance (pid, start time, binary
// path, the embedded proxy transaction). It is never trusted as the source of
// truth for "what is the system doing now": that is always recomputed from live
// signals (process alive, port listening, proxy health, current proxy settings)
// via Determine.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"mihatch/internal/atomicfile"
	"mihatch/internal/proxy"
)

// SchemaVersion is the persisted state file schema version.
const SchemaVersion = 1

// State is a logical lifecycle state.
type State string

const (
	StateUninitialized State = "uninitialized"
	StateStopped       State = "stopped"  // no running process, port closed
	StateStandby       State = "standby"  // mihomo healthy, proxy not owned
	StateActive        State = "active"   // mihomo healthy, proxy owned & intact
	StateDegraded      State = "degraded" // process/port/health/ownership broken
)

// Facts are live signals. Determine maps them to a State (pure, testable).
type Facts struct {
	Initialized      bool
	BinaryExists     bool
	ProcessRunning   bool // pid alive and matches recorded identity
	PortListening    bool // mixed port accepts connections
	ProxyOK          bool // outbound health through the proxy succeeds
	OwnershipOwned   bool
	OwnershipDrifted bool
}

// Determine maps live Facts to a logical State.
func Determine(f Facts) State {
	if !f.Initialized || !f.BinaryExists {
		return StateUninitialized
	}
	if !f.ProcessRunning && !f.PortListening {
		return StateStopped
	}
	healthy := f.ProcessRunning && f.PortListening && f.ProxyOK
	if !healthy {
		return StateDegraded
	}
	if f.OwnershipDrifted {
		return StateDegraded
	}
	if f.OwnershipOwned {
		return StateActive
	}
	return StateStandby
}

// ProcessInfo records the identity of the managed mihomo process.
type ProcessInfo struct {
	PID        int    `json:"pid"`
	StartTime  string `json:"start_time,omitempty"` // ps lstart string, for identity
	BinaryPath string `json:"binary_path"`
	StartedAt  string `json:"started_at,omitempty"` // ISO timestamp we launched it
}

// Ownership records MiHatch's claim on the system proxy, including the full
// compare-before-restore transaction (Before + Applied) so restore can detect
// drift without a separate file.
type Ownership struct {
	Owned              bool                 `json:"owned"`
	TransactionID      string               `json:"transaction_id,omitempty"`
	AppliedFingerprint string               `json:"applied_fingerprint,omitempty"`
	Services           []string             `json:"services,omitempty"`
	AcquiredAt         string               `json:"acquired_at,omitempty"`
	Before             []proxy.ServiceProxy `json:"before,omitempty"`
	Applied            []proxy.ServiceProxy `json:"applied,omitempty"`
}

// SourceRef records where the runtime config was imported from, so "sync" can
// re-read the same source. Path is a redacted display string.
type SourceRef struct {
	Type string `json:"type,omitempty"` // clash-verge-rev | file
	Path string `json:"path,omitempty"` // redacted display path (or actual path for file)
}

// Persisted is the on-disk state.json.
type Persisted struct {
	SchemaVersion int          `json:"schema_version"`
	Initialized   bool         `json:"initialized"`
	InitAt        string       `json:"init_at,omitempty"`
	MixedPort     int          `json:"mixed_port,omitempty"`
	BinaryPath    string       `json:"binary_path,omitempty"`
	BinarySHA256  string       `json:"binary_sha256,omitempty"`
	CoreVersion   string       `json:"core_version,omitempty"`
	Process       *ProcessInfo `json:"process,omitempty"`
	Ownership     Ownership    `json:"ownership"`
	Source        SourceRef    `json:"source,omitempty"`
	LastState     State        `json:"last_state,omitempty"`
	LastUpAt      string       `json:"last_up_at,omitempty"`
	LastDownAt    string       `json:"last_down_at,omitempty"`
}

// Load reads state.json. A missing file returns a zero Persisted (uninitialized)
// with no error.
func Load(path string) (*Persisted, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Persisted{SchemaVersion: SchemaVersion}, nil
		}
		return nil, err
	}
	var p Persisted
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if p.SchemaVersion == 0 {
		p.SchemaVersion = SchemaVersion
	}
	return &p, nil
}

// Save writes state.json atomically with mode 0600.
func (p *Persisted) Save(path string) error {
	if p.SchemaVersion == 0 {
		p.SchemaVersion = SchemaVersion
	}
	out, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	out = append(out, '\n')
	return atomicfile.WriteFile(path, 0o600, out)
}

// MarkInitialized sets initialization provenance and the engine facts.
func (p *Persisted) MarkInitialized(now, binaryPath, sha, coreVersion string, mixedPort int) {
	p.Initialized = true
	p.InitAt = now
	p.BinaryPath = binaryPath
	p.BinarySHA256 = sha
	p.CoreVersion = coreVersion
	p.MixedPort = mixedPort
}

// MarkUp records a successful up transition.
func (p *Persisted) MarkUp(now string, proc *ProcessInfo, owned bool) {
	p.Process = proc
	p.LastUpAt = now
	if owned {
		p.LastState = StateActive
	} else {
		p.LastState = StateStandby
	}
}

// MarkDown clears the process and ownership and records the transition.
func (p *Persisted) MarkDown(now string) {
	p.LastDownAt = now
	p.LastState = StateStopped
	p.Process = nil
	p.Ownership = Ownership{}
}

// SetOwnership records or clears proxy ownership.
func (p *Persisted) SetOwnership(o Ownership) {
	if !o.Owned {
		p.Ownership = Ownership{}
		return
	}
	p.Ownership = o
	p.LastState = StateActive
}

// ClearOwnership drops the proxy claim but keeps the process (pause).
func (p *Persisted) ClearOwnership(now string) {
	p.Ownership = Ownership{}
	p.LastState = StateStandby
	_ = now
}
