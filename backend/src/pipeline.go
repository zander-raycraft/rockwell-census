package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"census/src/audit"
	"census/src/parse"
	"census/src/reconcile"
)

/*
Extractor turns a parsed document into an extraction plan.

* @brief: Extractor -> requests a plan without writing the database
* @returns: plan [Plan] -> mappings applied to every data row
*/
type Extractor interface {
	/*
		Extract returns a plan for every data row in the parsed document.

		* @brief: Extract -> requests an extraction plan for one parsed document
		* @params: ctx [context.Context] -> cancellation and deadline for the model call
		* @params: parsed [Parsed] -> preview, headers, and data rows
		* @params: openIssues [string] -> open-issue lines sent for emails only
		* @returns: plan [Plan] -> mappings, labels, and optional email resolution
		* @returns: raw []byte -> raw model response JSON
		* @returns: err [error] -> non-nil when the model call fails
	*/
	Extract(ctx context.Context, parsed Parsed, openIssues string) (plan Plan, raw []byte, err error)
}

/*
Run applies one document to a company and commits the next state.

* @brief: Run -> loads state, extracts a plan, reconciles, and saves
* @params: ctx [context.Context] -> cancellation and deadline for this run
* @params: companyName [string] -> company whose census is updated
* @params: document [Document] -> one census file or one email body
* @params: db [*DB] -> locked load and save for this company
* @params: ex [Extractor] -> model call that returns a plan
* @params: lg [*RunLog] -> handle that flushes step events before continuing
* @returns: state [State] -> state that was committed
* @returns: err [error] -> run failure; nonempty state means commit succeeded but logging failed
*/
func Run(ctx context.Context, companyName string, document Document, db *DB, ex Extractor, lg *RunLog) (State, error) {
	if lg == nil {
		return State{}, fmt.Errorf("read: log is nil")
	}
	if db == nil {
		return State{}, fmt.Errorf("read: database is nil")
	}
	if ex == nil {
		return State{}, fmt.Errorf("extract: extractor is nil")
	}
	if strings.TrimSpace(companyName) == "" {
		return State{}, fmt.Errorf("read: company is required")
	}
	if audit.FromContext(ctx) == nil {
		recorder, err := audit.New(lg.Path(), lg.ID())
		if err != nil {
			return State{}, err
		}
		ctx = audit.WithRecorder(ctx, recorder)
	}
	lg.traceCtx = ctx
	lat := newLatency()
	if err := ctx.Err(); err != nil {
		return State{}, failStage(lg, lat, "READ", time.Now(), fmt.Errorf("read: %w", err))
	}

	lockStarted := time.Now().UTC()
	if err := traceInput(ctx, "LOCK", "reconcile.DB.LockCompany", companyName); err != nil {
		return State{}, err
	}
	unlock := db.LockCompany(companyName)
	defer unlock()
	if err := audit.Record(ctx, audit.Event{Component: "pipeline", Operation: "LOCK", Status: "finished", StartedAt: lockStarted, EndedAt: time.Now().UTC(), Output: "company lock acquired"}); err != nil {
		return State{}, err
	}

	if err := stepStarted(lg, "READ", "loading employees, dependents, enrollments, and open issues for "+companyName); err != nil {
		return State{}, err
	}
	started := time.Now()
	if err := traceInput(ctx, "READ", "reconcile.DB.Load", companyName); err != nil {
		return State{}, err
	}
	loaded, err := db.Load(ctx, companyName)
	if err != nil {
		return State{}, failStage(lg, lat, "READ", started, fmt.Errorf("read: %w", err))
	}
	state := reconcile.CopyState(loaded)
	if state.CompanyName == "" {
		state.CompanyName = companyName
	}
	if err := stepFinishedJSON(lg, lat, "READ", started, readWhat(state), state); err != nil {
		return State{}, fmt.Errorf("read: %w", err)
	}

	if err := recordPriorHistory(ctx, lg, state); err != nil {
		return State{}, fmt.Errorf("history: %w", err)
	}

	doc := cloneDocument(document)
	if err := ctx.Err(); err != nil {
		return State{}, failStage(lg, lat, "PARSE", time.Now(), fmt.Errorf("parse: %w", err))
	}
	if err := stepStarted(lg, "PARSE", "parsing "+doc.Filename); err != nil {
		return State{}, err
	}
	started = time.Now()
	if err := traceInput(ctx, "PARSE", "parse.Parse", doc); err != nil {
		return State{}, err
	}
	parsed, err := parse.Parse(doc)
	if err != nil {
		return State{}, failStage(lg, lat, "PARSE", started, fmt.Errorf("parse: %w", err))
	}
	if err := stepFinishedJSON(lg, lat, "PARSE", started, parseWhat(parsed), parseView(parsed)); err != nil {
		return State{}, fmt.Errorf("parse: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return State{}, failStage(lg, lat, "EXTRACT", time.Now(), fmt.Errorf("extract: %w", err))
	}
	if err := stepStarted(lg, "EXTRACT", "sending the parsed preview to the model for an extraction plan"); err != nil {
		return State{}, err
	}
	started = time.Now()
	if err := traceInput(ctx, "EXTRACT", "Extractor.Extract", map[string]any{"parsed": parsed, "open_issues": openIssueSummary(state)}); err != nil {
		return State{}, err
	}
	plan, raw, err := ex.Extract(ctx, parsed, openIssueSummary(state))
	if len(raw) > 0 {
		if logErr := lg.Event("EXTRACT", "response", "raw model response", string(raw), time.Since(started)); logErr != nil {
			return State{}, errors.Join(err, logErr)
		}
	}
	if err != nil {
		return State{}, failStage(lg, lat, "EXTRACT", started, err)
	}
	plan = reconcile.NormalizePlan(plan, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		err = fmt.Errorf("extract: invalid plan: %w", err)
		return State{}, failStage(lg, lat, "EXTRACT", started, err)
	}
	if err := stepFinishedJSON(lg, lat, "EXTRACT", started, planWhat(plan), plan); err != nil {
		return State{}, fmt.Errorf("extract: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return State{}, failStage(lg, lat, "FORMAT", time.Now(), fmt.Errorf("format: %w", err))
	}
	if err := stepStarted(lg, "FORMAT", "applying the extraction plan to every parsed data row"); err != nil {
		return State{}, err
	}
	started = time.Now()
	if err := traceInput(ctx, "FORMAT", "reconcile.Format", map[string]any{"plan": plan, "parsed": parsed, "company_id": state.CompanyID}); err != nil {
		return State{}, err
	}
	formatted, err := reconcile.Format(plan, parsed, state.CompanyID)
	if err != nil {
		return State{}, failStage(lg, lat, "FORMAT", started, fmt.Errorf("format: %w", err))
	}
	if parsed.Kind == "email" {
		formatted.DocumentText = string(doc.Body)
	}
	if doc.SourceSystem != "" {
		for i := range formatted.People {
			formatted.People[i].SourceNamespace = "system:" + strings.TrimSpace(doc.SourceSystem)
		}
	}
	if err := stepFinishedJSON(lg, lat, "FORMAT", started, formatWhat(formatted), formatted); err != nil {
		return State{}, fmt.Errorf("format: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return State{}, failStage(lg, lat, "MAP", time.Now(), fmt.Errorf("map: %w", err))
	}
	if err := stepStarted(lg, "MAP", "matching formatted people onto the current employees, dependents, and open issues"); err != nil {
		return State{}, err
	}
	started = time.Now()
	if err := traceInput(ctx, "MAP", "reconcile.Reconcile", map[string]any{"state": state, "formatted": formatted, "plan": plan}); err != nil {
		return State{}, err
	}
	next, decisions := reconcile.Reconcile(state, formatted, plan)
	if next.CompanyName == "" {
		next.CompanyName = companyName
	}
	if err := stepFinished(lg, lat, "MAP", started, mapWhat(decisions), strings.Join(decisions, "\n")); err != nil {
		return State{}, err
	}

	if err := audit.Record(ctx, audit.Event{Component: "pipeline", Operation: "MAP", Status: "result", StartedAt: started, EndedAt: time.Now().UTC(), Output: next}); err != nil {
		return State{}, err
	}
	if err := ctx.Err(); err != nil {
		return State{}, failStage(lg, lat, "WRITE", time.Now(), fmt.Errorf("write: %w", err))
	}
	if err := stepStarted(lg, "WRITE", "committing employees, dependents, enrollments, and open issues in one transaction"); err != nil {
		return State{}, err
	}
	started = time.Now()
	if err := traceInput(ctx, "WRITE", "reconcile.DB.Save", map[string]any{"state": next, "document": doc}); err != nil {
		return State{}, err
	}
	committed, err := db.Save(ctx, next, doc)
	if err != nil {
		if committed.CompanyID != "" {
			return committed, fmt.Errorf("state committed but database audit failed: %w", err)
		}
		return State{}, failStage(lg, lat, "WRITE", started, fmt.Errorf("write: %w", err))
	}
	if err := stepFinishedJSON(lg, lat, "WRITE", started, readWhat(committed), committed); err != nil {
		return committed, fmt.Errorf("state committed but write logging failed: %w", err)
	}
	if err := writeIssues(ctx, lg, committed); err != nil {
		return committed, fmt.Errorf("state committed but issue report failed: %w", err)
	}
	if err := recordCurrentHistory(ctx, lg, committed.CompanyID); err != nil {
		return committed, fmt.Errorf("state committed but history logging failed: %w", err)
	}
	if err := stepFinished(lg, lat, "LATENCY", lat.runStart, "stage durations for this run", lat.text()); err != nil {
		return committed, fmt.Errorf("state committed but latency logging failed: %w", err)
	}
	return committed, nil
}

/*
latency records how long each pipeline stage took.

* @brief: latency -> elapsed milliseconds for the stages of one run
* @returns: record [latency] -> stage names and durations from the start of the run
*/
type latency struct {
	runStart time.Time
	lines    []string
}

/*
newLatency starts the clock for one run.

* @brief: newLatency -> starts the run clock
* @returns: lat [*latency] -> empty stage list with the clock running
*/
func newLatency() *latency {
	return &latency{runStart: time.Now(), lines: []string{}}
}

/*
add records one stage duration in milliseconds.

* @brief: add -> appends one stage duration
* @params: name [string] -> stage name
* @params: started [time.Time] -> time the stage began
* @returns: elapsed [time.Duration] -> time since started
*/
func (lat *latency) add(name string, started time.Time) time.Duration {
	elapsed := time.Since(started)
	if lat.lines == nil {
		lat.lines = []string{}
	}
	lat.lines = append(lat.lines, fmt.Sprintf("%s %dms", name, elapsed.Milliseconds()))
	return elapsed
}

/*
text renders the stage durations and the total run time.

* @brief: text -> renders stage durations and the total
* @returns: body [string] -> one duration per line, then TOTAL
*/
func (lat *latency) text() string {
	if lat == nil {
		return "TOTAL 0ms\n"
	}
	body := strings.Join(lat.lines, "\n")
	if body != "" {
		body += "\n"
	}
	body += fmt.Sprintf("TOTAL %dms\n", time.Since(lat.runStart).Milliseconds())
	return body
}

/*
stepStarted writes a line that this step is running.

* @brief: stepStarted -> records that a pipeline step has started
* @params: lg [*RunLog] -> run log handle
* @params: step [string] -> step name
* @params: what [string] -> what this step is doing
* @returns: err [error] -> pipeline or logging failure
*/
func stepStarted(lg *RunLog, step, what string) error {
	return lg.Event(step, "started", what, "", 0)
}

/*
stepFinished writes the step result and its latency.

* @brief: stepFinished -> records that a pipeline step finished and how long it took
* @params: lg [*RunLog] -> run log handle
* @params: lat [*latency] -> duration list for this run
* @params: step [string] -> step name
* @params: started [time.Time] -> time the step began
* @params: what [string] -> what the step produced
* @params: body [string] -> detail appended under the summary
* @returns: err [error] -> pipeline or logging failure
*/
func stepFinished(lg *RunLog, lat *latency, step string, started time.Time, what, body string) error {
	elapsed := lat.add(step, started)
	return lg.Event(step, "finished", what, body, elapsed)
}

/*
stepFinishedJSON writes a finished step with an indented JSON body.

* @brief: stepFinishedJSON -> records a finished step, its latency, and a JSON body
* @params: lg [*RunLog] -> run log handle
* @params: lat [*latency] -> duration list for this run
* @params: step [string] -> step name
* @params: started [time.Time] -> time the step began
* @params: what [string] -> what the step produced
* @params: value [any] -> value encoded under the summary
* @returns: err [error] -> non-nil when encoding or logging fails
*/
func stepFinishedJSON(lg *RunLog, lat *latency, step string, started time.Time, what string, value any) error {
	blob, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return stepFinished(lg, lat, step, started, what, string(blob))
}

/*
failStage writes a failed step and the durations collected so far.

* @brief: failStage -> records a failed step and the latency summary
* @params: lg [*RunLog] -> run log handle
* @params: lat [*latency] -> durations collected before the failure
* @params: step [string] -> step that failed
* @params: started [time.Time] -> time the failed step began
* @params: err [error] -> step error
* @returns: err [error] -> pipeline or logging failure
*/
func failStage(lg *RunLog, lat *latency, step string, started time.Time, err error) error {
	elapsed := lat.add(step, started)
	return errors.Join(err,
		lg.Event(step, "failed", err.Error(), "", elapsed),
		lg.Event("LATENCY", "finished", "run failed before commit", lat.text(), time.Since(lat.runStart)))
}

/*
openIssueSummary lists kind and detail for current open issues.

* @brief: openIssueSummary -> formats open issues for the model
* @params: state [State] -> loaded company state
* @returns: summary [string] -> kind and detail lines, or empty when none are open
*/
func openIssueSummary(state State) string {
	var b strings.Builder
	n := 0
	for _, issue := range state.OpenIssues {
		if issue.Status != "" && !strings.EqualFold(issue.Status, "OPEN") {
			continue
		}
		n++
		if n > 50 {
			b.WriteString("(further open issues omitted)\n")
			break
		}
		b.WriteString("- " + issue.Kind + ": " + issue.Detail + "\n")
	}
	return b.String()
}

/*
readWhat summarizes how many census rows a state holds.

* @brief: readWhat -> counts the tables in a state
* @params: state [State] -> census state
* @returns: what [string] -> employee, dependent, enrollment, and issue counts
*/
func readWhat(state State) string {
	return fmt.Sprintf("employees %d, dependents %d, enrollments %d, open issues %d",
		len(state.Employees), len(state.Dependents), len(state.Enrollments), len(state.OpenIssues))
}

/*
parseWhat summarizes the parsed document shape.

* @brief: parseWhat -> describes the parsed file
* @params: parsed [Parsed] -> parser result
* @returns: what [string] -> kind, delimiter, header row, data rows, and skipped rows
*/
func parseWhat(parsed Parsed) string {
	delim := parsed.Delimiter
	if delim == "" {
		delim = "none"
	}
	return fmt.Sprintf("%s delimiter %s, header row %d, %d data rows, %d skipped",
		parsed.Kind, delim, parsed.HeaderRow, len(parsed.Rows), len(parsed.Skipped))
}

/*
planWhat summarizes the extraction plan.

* @brief: planWhat -> describes the plan the model returned
* @params: plan [Plan] -> extraction plan
* @returns: what [string] -> document type, date format, and mapping counts
*/
func planWhat(plan Plan) string {
	return fmt.Sprintf("document %s, dates %s, %d mappings, %d labels, %d uncertainties",
		plan.DocumentType, plan.DateFormat, len(plan.Mappings), len(plan.Labels), len(plan.Uncertainties))
}

/*
formatWhat summarizes the formatted people.

* @brief: formatWhat -> counts formatted people and row issues
* @params: formatted [Formatted] -> formatter result
* @returns: what [string] -> people and issue counts
*/
func formatWhat(formatted Formatted) string {
	return fmt.Sprintf("%d people, %d row issues", len(formatted.People), len(formatted.Issues))
}

/*
mapWhat summarizes reconciler decisions.

* @brief: mapWhat -> counts match, add, and issue decisions
* @params: decisions [[]string] -> reconciler decision lines
* @returns: what [string] -> decision count
*/
func mapWhat(decisions []string) string {
	return fmt.Sprintf("%d match and issue decisions", len(decisions))
}

/*
parseView is the PARSE log payload, including every data row.

* @brief: parseView -> copies the parsed document for the log
* @params: parsed [Parsed] -> parser result
* @returns: view [parseLog] -> kind, header, skipped notes, preview, and rows
*/
func parseView(parsed Parsed) parseLog {
	return parseLog{
		Kind:      parsed.Kind,
		Delimiter: parsed.Delimiter,
		HeaderRow: parsed.HeaderRow,
		Headers:   append([]string(nil), parsed.Headers...),
		RowCount:  len(parsed.Rows),
		Skipped:   append([]string(nil), parsed.Skipped...),
		Preview:   parsed.Preview,
		Rows:      copyRows(parsed.Rows),
	}
}

/*
cloneDocument copies the document bytes before they cross into parse and save.

* @brief: cloneDocument -> copies upload bytes
* @params: document [Document] -> original upload
* @returns: copy [Document] -> document with its own body slice
*/
func cloneDocument(document Document) Document {
	document.Body = append([]byte(nil), document.Body...)
	return document
}

/*
copyRows copies parsed data rows so a later stage cannot edit the parser result.

* @brief: copyRows -> deep-copies string rows
* @params: rows [[][]string] -> parsed data rows
* @returns: copy [[][]string] -> independent rows
*/
func copyRows(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for i, row := range rows {
		out[i] = append([]string(nil), row...)
	}
	return out
}

type parseLog struct {
	Kind      string     `json:"kind"`
	Delimiter string     `json:"delimiter"`
	HeaderRow int        `json:"header_row"`
	Headers   []string   `json:"headers"`
	RowCount  int        `json:"row_count"`
	Skipped   []string   `json:"skipped"`
	Preview   string     `json:"preview"`
	Rows      [][]string `json:"rows"`
}

/*
traceInput preserves the actual arguments at a pipeline function boundary.

* @brief: traceInput -> records the function and its unabridged input
* @params: ctx [context.Context] -> run trace context
* @params: step [string] -> pipeline stage
* @params: function [string] -> called function
* @params: value [any] -> arguments passed to that function
* @returns: err [error] -> trace failure before the call
*/
func traceInput(ctx context.Context, step, function string, value any) error {
	return audit.Record(ctx, audit.Event{Component: "pipeline", Operation: step, Status: "input",
		StartedAt: time.Now().UTC(), Input: map[string]any{"function": function, "go_type": fmt.Sprintf("%T", value), "arguments": value}})
}
