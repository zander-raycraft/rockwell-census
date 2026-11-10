package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

/*
TestRemoveRun hides only the chosen finished job and preserves its original audit trace.

* @brief: TestRemoveRun -> verifies company history removal without deleting evidence
* @params: t [*testing.T] -> test result
*/
func TestRemoveRun(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"failed-run", "completed-run", "active-run"} {
		folder := filepath.Join(dir, id)
		if err := os.Mkdir(folder, 0700); err != nil {
			t.Fatal(err)
		}
		body := `{"component":"http","operation":"request","started_at":"2026-11-10T12:00:00Z","input":{"endpoint":"/run","company":"Example","filename":"census.csv"}}` + "\n"
		if id != "active-run" {
			status := 200
			if id == "failed-run" {
				status = 500
			}
			body += fmt.Sprintf("{\"component\":\"http\",\"operation\":\"completion\",\"output\":{\"status\":%d}}\n", status)
		}
		if err := os.WriteFile(filepath.Join(folder, "audit.jsonl"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{logDir: dir}
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /runs/{id}", s.handleRemoveRun)
	mux.HandleFunc("GET /runs", s.handleRuns)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("DELETE", "/runs/failed-run", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("remove failed run = %d, %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "failed-run", "audit.jsonl")); err != nil {
		t.Fatalf("original evidence removed: %v", err)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/runs?company=Example", nil))
	var runs []runListing
	if err := json.Unmarshal(response.Body.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("visible runs = %+v, want completed and active", runs)
	}
	for _, run := range runs {
		if run.ID == "failed-run" {
			t.Fatal("hidden run is still listed")
		}
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("DELETE", "/runs/active-run", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("remove active run = %d, want conflict", response.Code)
	}
}
