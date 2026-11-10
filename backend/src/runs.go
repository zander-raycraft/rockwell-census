package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type runListing struct {
	ID        string    `json:"id"`
	Company   string    `json:"company"`
	Filename  string    `json:"filename"`
	StartedAt time.Time `json:"started_at"`
	Status    string    `json:"status"`
}

/*
handleRuns lists document runs from their existing traces without loading their payloads into the response.

* @brief: handleRuns -> exposes saved and active document runs
* @params: w [http.ResponseWriter] -> JSON response writer
* @params: r [*http.Request] -> optional exact company filter
* @api: /runs [GET] -> returns document run metadata, newest first
*/
func (s *server) handleRuns(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(s.logDir)
	if err != nil && !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "runs could not be listed"})
		return
	}
	runs := []runListing{}
	for _, entry := range entries {
		if r.Context().Err() != nil {
			return
		}
		if !entry.IsDir() {
			continue
		}
		run, ok := readRunListing(filepath.Join(s.logDir, entry.Name(), "audit.jsonl"))
		if ok && (r.URL.Query().Get("company") == "" || run.Company == r.URL.Query().Get("company")) {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt.After(runs[j].StartedAt) })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, runs)
}

/*
readRunListing reads request identity and the trace tail; old traces recover identity from their early pipeline events.

* @brief: readRunListing -> reads metadata without decoding embedded prior-run histories
* @params: path [string] -> existing audit.jsonl path
* @returns: run [runListing] -> identity and observed completion status
* @returns: ok [bool] -> trace identifies a document run and its company
*/
func readRunListing(path string) (run runListing, ok bool) {
	file, err := os.Open(path)
	if err != nil {
		return run, false
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	for i := 0; i < 16; i++ {
		var event struct {
			Component string          `json:"component"`
			Operation string          `json:"operation"`
			Status    string          `json:"status"`
			StartedAt time.Time       `json:"started_at"`
			Input     json.RawMessage `json:"input"`
			Output    json.RawMessage `json:"output"`
		}
		if decoder.Decode(&event) != nil {
			break
		}
		if i == 0 {
			var request struct{ Endpoint, Company, Filename string }
			if json.Unmarshal(event.Input, &request) != nil || request.Endpoint != "/run" {
				return run, false
			}
			run = runListing{ID: filepath.Base(filepath.Dir(path)), Company: request.Company, Filename: request.Filename, StartedAt: event.StartedAt, Status: "running"}
		}
		if run.Company != "" && run.Filename != "" {
			break
		}
		if event.Component == "pipeline" && event.Operation == "READ" && event.Status == "finished" {
			var state State
			if json.Unmarshal(event.Output, &state) == nil {
				run.Company = state.CompanyName
			}
			break
		}
		if event.Component == "history" {
			break
		}
	}
	if run.Company == "" {
		return run, false
	}
	if run.Filename == "" {
		parts := strings.SplitN(run.ID, "_", 4)
		if len(parts) == 4 {
			run.Filename = parts[3]
		}
	}
	info, err := file.Stat()
	if err != nil {
		return run, false
	}
	offset := max(int64(0), info.Size()-65536)
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return run, false
	}
	tail, err := io.ReadAll(file)
	if err != nil {
		return run, false
	}
	lines := bytes.Split(tail, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		var event struct {
			Component string               `json:"component"`
			Operation string               `json:"operation"`
			Output    struct{ Status int } `json:"output"`
		}
		if json.Unmarshal(lines[i], &event) == nil && event.Component == "http" && (event.Operation == "response_ready" || event.Operation == "transport_write" || event.Operation == "completion") {
			run.Status = "completed"
			if event.Output.Status >= 400 {
				run.Status = "failed"
			}
			return run, true
		}
	}
	if time.Since(info.ModTime()) > 5*time.Minute {
		run.Status = "unknown"
	}
	return run, true
}
