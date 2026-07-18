package runner

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// Fake is a test Runner that returns scripted results without invoking any
// process. It records every call for assertion.
//
// Scripting modes:
//   - DefaultResult: returned when no per-key entry matches.
//   - Exact map keyed by "name arg1 arg2" (joined by single spaces).
//   - Func: an arbitrary predicate over (name, args) for richer behavior.
type Fake struct {
	mu        sync.Mutex
	Default   *Result
	DefaultOK bool // when true, an unscripted call returns an empty success result
	Exact     map[string]FakeEntry
	Func      func(ctx context.Context, name string, args []string) (Result, error)
	Calls     []Call
}

// FakeEntry pairs a scripted result with an error.
type FakeEntry struct {
	Result Result
	Err    error
}

// Call records one Run invocation.
type Call struct {
	Name string
	Args []string
}

// Key builds the Exact-map key for a name+args pair.
func Key(name string, args []string) string {
	parts := append([]string{name}, args...)
	return strings.Join(parts, " ")
}

// Run implements Runner.
func (f *Fake) Run(ctx context.Context, name string, args ...string) (Result, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, Call{Name: name, Args: append([]string(nil), args...)})
	f.mu.Unlock()

	if ctx.Err() != nil {
		return Result{ExitCode: -1}, ctx.Err()
	}
	if f.Func != nil {
		return f.Func(ctx, name, args)
	}
	if entry, ok := f.Exact[Key(name, args)]; ok {
		return entry.Result, entry.Err
	}
	if f.Default != nil {
		return *f.Default, nil
	}
	if f.DefaultOK {
		return Result{}, nil
	}
	return Result{ExitCode: -1}, &ExecError{
		Name: name,
		Args: args,
		Err:  errors.New("fake: unscripted command (set Fake.Default or Fake.Exact)"),
	}
}

// CallCount returns the number of recorded invocations.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

// Lookup returns the n-th recorded call (zero-indexed) or false.
func (f *Fake) Lookup(n int) (Call, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < 0 || n >= len(f.Calls) {
		return Call{}, false
	}
	return f.Calls[n], true
}

// Saw reports whether any recorded call matches name and all args in order.
func (f *Fake) Saw(name string, args ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		if c.Name != name {
			continue
		}
		if len(c.Args) != len(args) {
			continue
		}
		match := true
		for i := range args {
			if c.Args[i] != args[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
