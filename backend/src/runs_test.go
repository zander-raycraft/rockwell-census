package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
TestReadRunListing checks live discovery and completion after payloads larger than the trace tail.

* @brief: TestReadRunListing -> verifies run metadata comes from trace evidence
* @params: t [*testing.T] -> test result
*/
func TestReadRunListing(t *testing.T) {
	for _, test := range []struct{ name, tail, want string }{
		{"active", "", "running"},
		{"completed", `{"component":"http","operation":"completion","output":{"status":200}}` + "\n", "completed"},
		{"failed", `{"component":"http","operation":"completion","output":{"status":500}}` + "\n", "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audit.jsonl")
			body := `{"component":"http","operation":"request","started_at":"2026-11-10T12:00:00Z","input":{"endpoint":"/run","company":"Exact Company","filename":"document.csv"}}` + "\n"
			body += `{"component":"pipeline","output":"` + strings.Repeat("x", 70000) + `"}` + "\n" + test.tail
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			run, ok := readRunListing(path)
			if !ok || run.Company != "Exact Company" || run.Filename != "document.csv" || run.Status != test.want {
				t.Fatalf("readRunListing(%s) = %+v, %v; want exact company, filename and status %s", test.name, run, ok, test.want)
			}
		})
	}
}
