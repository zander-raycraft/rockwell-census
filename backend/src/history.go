package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"census/src/audit"
)

type historyEvent struct {
	Component string          `json:"component"`
	Operation string          `json:"operation"`
	Status    string          `json:"status"`
	Timestamp time.Time       `json:"timestamp"`
	Input     json.RawMessage `json:"input"`
	Output    json.RawMessage `json:"output"`
}

type documentHistory struct {
	Filename   string `json:"filename,omitempty"`
	Checksum   string `json:"checksum_sha256,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
}

type factChange struct {
	Table  string          `json:"table"`
	RowID  string          `json:"row_id"`
	Field  string          `json:"field"`
	Action string          `json:"action"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

type historyRun struct {
	RunID         string            `json:"run_id"`
	At            time.Time         `json:"at"`
	Outcome       string            `json:"outcome"`
	Document      documentHistory   `json:"trigger_document"`
	Changes       []factChange      `json:"fact_changes"`
	Events        []json.RawMessage `json:"events,omitempty"`
	LegacyLogs    map[string]string `json:"legacy_stage_logs,omitempty"`
	Gap           string            `json:"history_gap,omitempty"`
	before, after State
	read, written bool
}

/*
readHistoryRun preserves original trace events and derives only committed state changes.

* @brief: readHistoryRun -> reads one run without recursively copying earlier history views
* @params: dir [string] -> run folder
* @params: companyID [string] -> exact company identity required for inclusion
* @returns: run [historyRun] -> available evidence and state transitions
* @returns: matches [bool] -> true when the run read this company
* @returns: err [error] -> filesystem failure reading available evidence
*/
func readHistoryRun(dir, companyID string) (run historyRun, matches bool, err error) {
	run.RunID = filepath.Base(dir)
	run.Outcome = "incomplete"
	body, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		run.LegacyLogs = map[string]string{}
		run.Gap = "legacy logs: HTTP/model request/SQL boundary capture unavailable"
		for _, stage := range []string{"READ", "PARSE", "EXTRACT", "FORMAT", "MAP", "WRITE", "LATENCY"} {
			data, readErr := os.ReadFile(filepath.Join(dir, stepFile(stage)))
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return run, false, readErr
			}
			run.LegacyLogs[stage] = string(data)
			if stage != "READ" && stage != "WRITE" {
				continue
			}
			marker := []byte("== " + stage + " finished ==")
			section := bytes.SplitN(data, marker, 2)
			if len(section) != 2 {
				continue
			}
			start := bytes.IndexByte(section[1], '{')
			if start < 0 {
				continue
			}
			var state State
			if decodeErr := json.NewDecoder(bytes.NewReader(section[1][start:])).Decode(&state); decodeErr != nil {
				run.Gap += "; invalid " + stage + " snapshot"
				continue
			}
			if stage == "READ" {
				run.before = state
				run.read = true
			} else {
				run.after = state
				run.written = true
			}
		}
		if info, statErr := os.Stat(filepath.Join(dir, stepFile("READ"))); statErr == nil {
			run.At = info.ModTime()
			run.Gap += "; ordering uses file modification time"
		}
	} else if err != nil {
		return run, false, err
	} else {
		decoder := json.NewDecoder(bytes.NewReader(body))
		for {
			var raw json.RawMessage
			decodeErr := decoder.Decode(&raw)
			if decodeErr == io.EOF {
				break
			}
			if decodeErr != nil {
				run.Gap = "trace incomplete or malformed: " + decodeErr.Error()
				break
			}
			var event historyEvent
			if decodeErr = json.Unmarshal(raw, &event); decodeErr != nil {
				return run, false, decodeErr
			}
			if event.Component == "history" {
				continue
			}
			run.Events = append(run.Events, raw)
			if run.At.IsZero() {
				run.At = event.Timestamp
			}
			if event.Component == "pipeline" && event.Status == "finished" {
				switch event.Operation {
				case "READ":
					err = json.Unmarshal(event.Output, &run.before)
					run.read = err == nil
				case "WRITE":
					err = json.Unmarshal(event.Output, &run.after)
					run.written = err == nil
				}
				if err != nil {
					run.Gap = "invalid state snapshot: " + err.Error()
					err = nil
				}
			}
			if event.Component == "pipeline" && event.Operation == "PARSE" && event.Status == "input" {
				var input struct {
					Arguments Document `json:"arguments"`
				}
				if decodeErr = json.Unmarshal(event.Input, &input); decodeErr != nil {
					return run, false, decodeErr
				}
				run.Document.Filename = input.Arguments.Filename
				run.Document.Checksum = fmt.Sprintf("%x", sha256.Sum256(input.Arguments.Body))
			}
			if event.Component == "database" && event.Operation == "scan" {
				var input struct {
					SQL string `json:"sql"`
				}
				var output struct {
					Columns []string `json:"columns"`
					Values  []struct {
						Value json.RawMessage `json:"value"`
					} `json:"values"`
				}
				if decodeErr = json.Unmarshal(event.Input, &input); decodeErr != nil {
					return run, false, decodeErr
				}
				if strings.Contains(input.SQL, "INSERT INTO documents") {
					if decodeErr = json.Unmarshal(event.Output, &output); decodeErr != nil {
						return run, false, decodeErr
					}
					for i, column := range output.Columns {
						if column == "id" && i < len(output.Values) {
							if decodeErr = json.Unmarshal(output.Values[i].Value, &run.Document.DocumentID); decodeErr != nil {
								return run, false, decodeErr
							}
						}
					}
				}
			}
			if event.Component == "database" && event.Operation == "commit" && event.Status == "completed" {
				var output struct {
					Committed bool `json:"committed"`
				}
				if decodeErr = json.Unmarshal(event.Output, &output); decodeErr != nil {
					return run, false, decodeErr
				}
				if output.Committed {
					run.Outcome = "committed"
					run.At = event.Timestamp
				}
			}
			if event.Component == "pipeline" && event.Status == "failed" && run.Outcome != "committed" {
				run.Outcome = "failed"
			}
		}
	}
	matches = run.read && run.before.CompanyID == companyID
	if !matches {
		return run, false, nil
	}
	if run.written {
		run.Outcome = "committed"
		run.Changes, err = changedFacts(run.before, run.after)
	} else if run.Outcome == "committed" {
		run.Gap += "; committed state snapshot unavailable"
	}
	return run, true, err
}

/*
changedFacts compares stored values by table, stable row id and field, not SQL reinsert counts.

* @brief: changedFacts -> derives additions, changes and removals from two state snapshots
* @params: before [State] -> state read before this document
* @params: after [State] -> committed state
* @returns: changes [[]factChange] -> deterministic field transitions; unchanged values omitted
* @returns: err [error] -> snapshot encoding failure
*/
func changedFacts(before, after State) (changes []factChange, err error) {
	changes = []factChange{}
	var states [2]map[string]json.RawMessage
	for i, state := range []State{before, after} {
		raw, encodeErr := json.Marshal(state)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if decodeErr := json.Unmarshal(raw, &states[i]); decodeErr != nil {
			return nil, decodeErr
		}
	}
	for _, table := range []string{"employees", "dependents", "enrollments", "open_issues"} {
		indexed := [2]map[string]map[string]json.RawMessage{}
		ids := map[string]bool{}
		for i := range states {
			indexed[i] = map[string]map[string]json.RawMessage{}
			var rows []map[string]json.RawMessage
			if decodeErr := json.Unmarshal(states[i][table], &rows); decodeErr != nil {
				return nil, decodeErr
			}
			for _, row := range rows {
				var id string
				if decodeErr := json.Unmarshal(row["id"], &id); decodeErr != nil {
					return nil, decodeErr
				}
				indexed[i][id] = row
				ids[id] = true
			}
		}
		for id := range ids {
			left, right := indexed[0][id], indexed[1][id]
			fields := map[string]bool{}
			for field := range left {
				fields[field] = true
			}
			for field := range right {
				fields[field] = true
			}
			for field := range fields {
				if field == "id" || field == "company_id" || field == "key" {
					continue
				}
				a, b := left[field], right[field]
				if a == nil {
					a = json.RawMessage("null")
				}
				if b == nil {
					b = json.RawMessage("null")
				}
				if bytes.Equal(a, b) {
					continue
				}
				action := "changed"
				if left == nil {
					action = "row_added"
				} else if right == nil {
					action = "row_removed"
				} else if string(a) == "null" {
					action = "filled"
				} else if string(b) == "null" {
					action = "cleared"
				}
				changes = append(changes, factChange{Table: table, RowID: id, Field: field, Action: action, Before: a, After: b})
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		if a.RowID != b.RowID {
			return a.RowID < b.RowID
		}
		return a.Field < b.Field
	})
	return changes, nil
}

/*
recordPriorHistory embeds available company runs in this run's single audit file.

* @brief: recordPriorHistory -> includes earlier evidence and committed changes without inventing origins
* @params: ctx [context.Context] -> current recorder context
* @params: lg [*RunLog] -> current run folder
* @params: state [State] -> company identity and currently stored facts
* @returns: err [error] -> reading or recording history failed before document processing
*/
func recordPriorHistory(ctx context.Context, lg *RunLog, state State) error {
	started := time.Now().UTC()
	dirs, err := os.ReadDir(filepath.Dir(lg.Path()))
	if err != nil {
		return err
	}
	runs := []historyRun{}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !dir.IsDir() || dir.Name() == lg.ID() || !strings.HasPrefix(dir.Name(), safeToken(state.CompanyName)+"_") {
			continue
		}
		run, matches, readErr := readHistoryRun(filepath.Join(filepath.Dir(lg.Path()), dir.Name()), state.CompanyID)
		if readErr != nil {
			return fmt.Errorf("history: %w", readErr)
		}
		if matches {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].At.Equal(runs[j].At) {
			return runs[i].RunID < runs[j].RunID
		}
		return runs[i].At.Before(runs[j].At)
	})
	baseline := state
	if len(runs) > 0 {
		baseline = runs[0].before
	}
	previous := baseline
	gaps := []map[string]any{}
	for _, run := range runs {
		changes, compareErr := changedFacts(previous, run.before)
		if compareErr != nil {
			return compareErr
		}
		if len(changes) > 0 {
			gaps = append(gaps, map[string]any{"before_run": run.RunID, "unexplained_changes": changes})
		}
		if run.written {
			previous = run.after
		}
	}
	changes, compareErr := changedFacts(previous, state)
	if compareErr != nil {
		return compareErr
	}
	if len(changes) > 0 {
		gaps = append(gaps, map[string]any{"before_run": lg.ID(), "unexplained_changes": changes})
	}
	return audit.Record(ctx, audit.Event{Component: "history", Operation: "prior_runs", Status: "finished", StartedAt: started, EndedAt: time.Now().UTC(), Output: map[string]any{
		"company_id": state.CompanyID, "runs": runs, "baseline_unknown_origin": baseline, "unexplained_transitions": gaps, "coverage": "available company traces only; earlier origin is unknown where evidence is absent",
		"attribution":      "trigger_document identifies the processing input, not proof that every changed fact originated in it; retained claims and merges remain in original events",
		"unchanged_values": "not attributed again merely because a save rewrites rows",
	}})
}

/*
recordCurrentHistory appends the committed field changes beside this run's original events.

* @brief: recordCurrentHistory -> records the latest temporal state transition
* @params: ctx [context.Context] -> current recorder context
* @params: lg [*RunLog] -> current run folder
* @params: companyID [string] -> exact company identity
* @returns: err [error] -> reading or appending history failed after commit
*/
func recordCurrentHistory(ctx context.Context, lg *RunLog, companyID string) error {
	started := time.Now().UTC()
	run, matches, err := readHistoryRun(lg.Path(), companyID)
	if err != nil {
		return err
	}
	if !matches || !run.written {
		return fmt.Errorf("history: committed snapshots unavailable")
	}
	run.Events = nil
	return audit.Record(ctx, audit.Event{Component: "history", Operation: "committed_changes", Status: "finished", StartedAt: started, EndedAt: time.Now().UTC(), Output: run})
}
