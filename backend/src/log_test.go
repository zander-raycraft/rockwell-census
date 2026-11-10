package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
TestRunLogsRemainDistinctAndImmediatelyReadable verifies same-input runs never share their trace.

* @brief: TestRunLogsRemainDistinctAndImmediatelyReadable -> checks isolated, synchronously visible logs
* @params: t [*testing.T] -> test reporter
*/
func TestRunLogsRemainDistinctAndImmediatelyReadable(t *testing.T) {
	logs := StartLogService()
	defer logs.Stop()
	dir := t.TempDir()
	ids := make(map[string]bool)
	for range 20 {
		run, err := logs.NewRun(dir, "Same Company", "same.csv")
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(run.Path())
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("NewRun directory permissions = %o; want 755 for host log readers", info.Mode().Perm())
		}
		if ids[run.ID()] {
			t.Fatalf("NewRun(same input) reused %q", run.ID())
		}
		ids[run.ID()] = true
		if err := run.Event("READ", "finished", run.ID(), "", 0); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(run.Path(), stepFile("READ")))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), run.ID()) {
			t.Fatalf("READ log = %q; want own run id %q before Event returns", body, run.ID())
		}
	}
}

/*
TestLogFailureReachesCaller verifies disk failures are returned with the original stage error.

* @brief: TestLogFailureReachesCaller -> checks disk errors cannot disappear into stderr
* @params: t [*testing.T] -> test reporter
*/
func TestLogFailureReachesCaller(t *testing.T) {
	logs := StartLogService()
	defer logs.Stop()
	run, err := logs.NewRun(t.TempDir(), "Company", "input.csv")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(run.Path(), stepFile("EXTRACT")), 0o755); err != nil {
		t.Fatal(err)
	}
	original := errors.New("model failed")
	err = failStage(run, newLatency(), "EXTRACT", newLatency().runStart, original)
	var diskErr *os.PathError
	if !errors.Is(err, original) || !errors.As(err, &diskErr) {
		t.Fatalf("failStage(unwritable log) = %v; want model and disk failures", err)
	}
	if _, err := os.Stat(filepath.Join(run.Path(), stepFile("LATENCY"))); err != nil {
		t.Fatalf("failure LATENCY log: %v", err)
	}
}
