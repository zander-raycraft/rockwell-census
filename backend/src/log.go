package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"census/src/audit"
	"census/src/reconcile"
)

/*
LogService serializes synchronous, durable run-log appends.

* @brief: LogService -> writes each block before the pipeline proceeds
* @returns: service [LogService] -> writer stopped with the process
*/
type LogService struct {
	mu      sync.Mutex
	stopped bool
}

/*
StartLogService creates the synchronous log writer.

* @brief: StartLogService -> creates the run-log writer
* @returns: service [*LogService] -> writer that accepts events until Stop
*/
func StartLogService() *LogService { return &LogService{} }

/*
Stop prevents subsequent writes after current appends finish.

* @brief: Stop -> stops accepting run-log writes
*/
func (s *LogService) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
}

/*
append writes and flushes one block before returning its disk errors.

* @brief: append -> appends one durable log block
* @params: path [string] -> step file path
* @params: text [string] -> complete log block
* @returns: err [error] -> open, write, sync, close, or stopped-writer failure
*/
func (s *LogService) append(path, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return fmt.Errorf("log: service is stopped")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	_, writeErr := f.WriteString(text)
	return errors.Join(writeErr, f.Sync(), f.Close())
}

/*
RunLog is the handle a census run uses to write step events.

* @brief: RunLog -> identity and path for one document's log file
* @returns: file [text] -> started and finished lines for each pipeline step
*/
type RunLog struct {
	id       string
	path     string
	svc      *LogService
	traceCtx context.Context
}

/*
NewRun atomically allocates a distinct folder for the company and document.

* @brief: NewRun -> creates a unique folder step files are written into
* @params: dir [string] -> directory that holds run folders
* @params: company [string] -> company name supplied with the document
* @params: filename [string] -> uploaded file name or email.txt
* @returns: log [*RunLog] -> handle that writes a file per step
* @returns: err [error] -> non-nil when the folder cannot be created
*/
func (s *LogService) NewRun(dir, company, filename string) (*RunLog, error) {
	if s == nil {
		return nil, fmt.Errorf("log: service is nil")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("log: %w", err)
	}
	path, err := os.MkdirTemp(dir, runFolder(company, filename, time.Now())+"-")
	if err != nil {
		return nil, fmt.Errorf("log: %w", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return nil, fmt.Errorf("log: %w", errors.Join(err, os.Remove(path)))
	}
	return &RunLog{id: filepath.Base(path), path: path, svc: s}, nil
}

/*
runFolder builds a readable folder name from the company, time, and file.

* @brief: runFolder -> joins a safe company, timestamp, and file name
* @params: company [string] -> company name
* @params: filename [string] -> document file name
* @params: now [time.Time] -> time the run started
* @returns: name [string] -> one path segment, with no slashes
*/
func runFolder(company, filename string, now time.Time) string {
	return safeToken(company) + "_" + now.Format("2006-01-02_150405") + "_" + safeToken(filename)
}

/*
safeToken keeps letters, digits, dots, and dashes so a name can be a file name.

* @brief: safeToken -> turns a label into a single path segment
* @params: value [string] -> company name or file name
* @returns: token [string] -> lowercase segment, at most 60 characters
*/
func safeToken(value string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' {
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
			continue
		}
		if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "document"
	}
	if len(out) > 60 {
		return strings.Trim(out[:60], "-")
	}
	return out
}

/*
stepFile is the file name a pipeline step writes as it runs.

* @brief: stepFile -> maps a step name to a numbered text file
* @params: step [string] -> pipeline step name
* @returns: name [string] -> file name inside the run folder
*/
func stepFile(step string) string {
	switch strings.ToUpper(strings.TrimSpace(step)) {
	case "READ":
		return "01-read.txt"
	case "PARSE":
		return "02-parse.txt"
	case "EXTRACT":
		return "03-extract.txt"
	case "FORMAT":
		return "04-format.txt"
	case "MAP":
		return "05-map.txt"
	case "WRITE":
		return "06-write.txt"
	case "LATENCY":
		return "07-latency.txt"
	default:
		return "99-" + safeToken(step) + ".txt"
	}
}

/*
Event writes and flushes one step record before returning.

* @brief: Event -> writes a step status, latency, and detail
* @params: step [string] -> pipeline step name
* @params: status [string] -> started, finished, or failed
* @params: what [string] -> one line describing the work
* @params: body [string] -> optional detail such as JSON or decision lines
* @params: elapsed [time.Duration] -> time the step took; ignored when status is started
* @returns: err [error] -> non-nil when the record could not be flushed
*/
func (l *RunLog) Event(step, status, what, body string, elapsed time.Duration) error {
	if l == nil || l.svc == nil {
		return fmt.Errorf("log: service is nil")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== %s %s ==\n", step, status)
	fmt.Fprintf(&b, "step: %s\n", step)
	fmt.Fprintf(&b, "status: %s\n", status)
	if status != "started" {
		fmt.Fprintf(&b, "elapsed_ms: %d\n", elapsed.Milliseconds())
	}
	fmt.Fprintf(&b, "what: %s\n", what)
	if strings.TrimSpace(body) != "" {
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		b.WriteString(body)
	}
	writeErr := l.svc.append(filepath.Join(l.path, stepFile(step)), b.String())
	if l.traceCtx == nil {
		return writeErr
	}
	ended := time.Now().UTC()
	var output any = body
	if json.Valid([]byte(body)) {
		output = json.RawMessage(body)
	}
	event := audit.Event{Component: "pipeline", Operation: step, Status: status,
		StartedAt: ended.Add(-elapsed), EndedAt: ended, Output: output}
	if status == "started" {
		event.EndedAt = time.Time{}
		event.Input = what
	}
	if status == "failed" {
		event.Error = what
	}
	return errors.Join(writeErr, audit.Record(l.traceCtx, event))
}

/*
ID returns the folder name for this run.

* @brief: ID -> returns the unique run folder
* @returns: id [string] -> company, timestamp, file name, and unique suffix
*/
func (l *RunLog) ID() string {
	if l == nil {
		return ""
	}
	return l.id
}

/*
Path returns the log file path.

* @brief: Path -> returns the log file path
* @returns: path [string] -> folder that holds the step files
*/
func (l *RunLog) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

/*
writeIssues writes the committed issue list as a readable per-run snapshot.

* @brief: writeIssues -> exports existing issue state without another tracking system
* @params: ctx [context.Context] -> current audit recorder
* @params: lg [*RunLog] -> unique run folder and durable writer
* @params: state [State] -> committed census and its open/resolved issues
* @returns: err [error] -> report or audit write failure after commit
*/
func writeIssues(ctx context.Context, lg *RunLog, state State) error {
	started := time.Now().UTC()
	summary := reconcile.Summarize(state)
	var body strings.Builder
	fmt.Fprintf(&body, "Company: %s\nRun: %s\nGenerated: %s\n", state.CompanyName, lg.ID(), started.Format(time.RFC3339Nano))
	fmt.Fprintf(&body, "Employees: %d | Dependents: %d | Held identities: %d | Members missing required fields: %d\n", summary.Employees, summary.Dependents, summary.HeldIdentities, summary.MembersMissingRequiredFields)
	fmt.Fprintf(&body, "Open issues: %d | Resolved issues: %d\n", summary.OpenIssues, summary.ResolvedIssues)
	names := map[string]string{}
	for _, employee := range state.Employees {
		names[employee.ID] = reconcile.DisplayName(employee.FirstName, employee.LastName)
	}
	for _, dependent := range state.Dependents {
		names[dependent.ID] = reconcile.DisplayName(dependent.FirstName, dependent.LastName)
	}
	issues := append([]OpenIssue(nil), state.OpenIssues...)
	sort.Slice(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		return a.Kind+"|"+a.Subject+"|"+a.ID < b.Kind+"|"+b.Subject+"|"+b.ID
	})
	for _, status := range []string{"OPEN", "RESOLVED"} {
		fmt.Fprintf(&body, "\n%s\n", status)
		count := 0
		for _, issue := range issues {
			resolved := issue.Status == "RESOLVED"
			if resolved != (status == "RESOLVED") {
				continue
			}
			count++
			fmt.Fprintf(&body, "\n%d. [%s] %s\n   ID: %s\n", count, issue.Kind, issue.Detail, issue.ID)
			name := names[issue.Subject]
			if name == "" && issue.EmployeeID != nil {
				name = names[*issue.EmployeeID]
			}
			if name != "" {
				fmt.Fprintf(&body, "   Affected record: %s\n", name)
			}
			if issue.Subject != "" {
				fmt.Fprintf(&body, "   Subject: %s\n", issue.Subject)
			}
			if issue.Field != "" {
				fmt.Fprintf(&body, "   Field: %s\n", issue.Field)
			}
			for index, raw := range issue.Claims {
				fmt.Fprintf(&body, "   Claim %d:\n", index+1)
				var document struct {
					Text          string   `json:"document_text"`
					Intent        string   `json:"intent"`
					Uncertainties []string `json:"uncertainties"`
				}
				if json.Unmarshal([]byte(raw), &document) == nil && document.Text != "" {
					fmt.Fprintf(&body, "   Intent: %s\n%s\n", document.Intent, document.Text)
					for _, note := range document.Uncertainties {
						fmt.Fprintf(&body, "   Review note: %s\n", note)
					}
				} else {
					fmt.Fprintf(&body, "%s\n", raw)
				}
			}
		}
		if count == 0 {
			body.WriteString("None.\n")
		}
	}
	text := body.String()
	if err := lg.svc.append(filepath.Join(lg.Path(), "issues.txt"), text); err != nil {
		return err
	}
	return audit.Record(ctx, audit.Event{Component: "issues", Operation: "snapshot", Status: "finished", StartedAt: started, EndedAt: time.Now().UTC(), Output: map[string]any{
		"file": "issues.txt", "company_id": state.CompanyID, "open": summary.OpenIssues, "resolved": summary.ResolvedIssues, "text": text,
	}})
}
