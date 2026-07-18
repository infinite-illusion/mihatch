package runner

import (
	"context"
	"strings"
	"testing"
)

func TestFakeScriptedExact(t *testing.T) {
	t.Parallel()
	f := &Fake{
		Exact: map[string]FakeEntry{
			"networksetup -getwebproxy Wi-Fi": {
				Result: Result{Stdout: []byte("Enabled: Yes\nServer: 127.0.0.1\nPort: 17890\n")},
			},
		},
		DefaultOK: true,
	}
	res, err := f.Run(context.Background(), "networksetup", "-getwebproxy", "Wi-Fi")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(res.Stdout), "127.0.0.1") {
		t.Fatalf("unexpected stdout: %s", res.Stdout)
	}
	if !f.Saw("networksetup", "-getwebproxy", "Wi-Fi") {
		t.Fatalf("call not recorded")
	}
	if f.CallCount() != 1 {
		t.Fatalf("CallCount=%d want 1", f.CallCount())
	}
}

func TestFakeUnscriptedErrors(t *testing.T) {
	t.Parallel()
	f := &Fake{}
	if _, err := f.Run(context.Background(), "nope"); err == nil {
		t.Fatal("expected unscripted error")
	}
}

func TestExecErrorPreservesExitCode(t *testing.T) {
	t.Parallel()
	ee := &ExecError{Name: "mihomo", Args: []string{"-t"}, Result: Result{ExitCode: 2, Stderr: []byte("bad yaml")}}
	if ExitCode(ee) != 2 {
		t.Fatalf("ExitCode=%d want 2", ExitCode(ee))
	}
	if !strings.Contains(ee.Error(), "exited 2") {
		t.Fatalf("error string missing exit code: %s", ee.Error())
	}
}
