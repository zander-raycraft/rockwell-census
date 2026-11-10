package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"census/src/reconcile"
)

/*
fixedPlanExtractor supplies a complete extraction plan without a model request.

* @brief: fixedPlanExtractor -> offline implementation of the pipeline's extractor
* @returns: extractor [fixedPlanExtractor] -> predetermined extraction plan
*/
type fixedPlanExtractor struct{ plan Plan }

/*
Extract returns the configured plan and its JSON as an offline model response.

* @brief: Extract -> returns fixed extraction rules without accessing a network
* @params: ctx [context.Context] -> request cancellation
* @params: parsed [Parsed] -> unused parsed document
* @params: openIssues [string] -> unused open-issue summary
* @returns: plan [Plan] -> configured extraction rules
* @returns: raw [[]byte] -> serialized plan for the extraction log
* @returns: err [error] -> cancellation or serialization failure
*/
func (e fixedPlanExtractor) Extract(ctx context.Context, parsed Parsed, openIssues string) (Plan, []byte, error) {
	if err := ctx.Err(); err != nil {
		return Plan{}, nil, err
	}
	raw, err := json.Marshal(e.plan)
	return e.plan, raw, err
}

/*
TestPipelineHTTPReportsCommittedState verifies API, logs, storage, and failure atomicity together.

* @brief: TestPipelineHTTPReportsCommittedState -> keeps successful and failed run evidence consistent
* @params: t [*testing.T] -> test reporter and optional isolated PostgreSQL configuration
*/
func TestPipelineHTTPReportsCommittedState(t *testing.T) {
	databaseURL := os.Getenv("CENSUS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set CENSUS_TEST_DATABASE_URL to an isolated PostgreSQL database")
	}
	ctx := context.Background()
	db, err := reconcile.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../migrations"); err != nil {
		t.Fatal(err)
	}
	suffix, err := reconcile.CensusUUID()
	if err != nil {
		t.Fatal(err)
	}
	initial, err := db.Load(ctx, "http-test-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.RemoveCompany(ctx, initial.CompanyID); err != nil {
			t.Error(err)
		}
	}()
	plan := Plan{DocumentType: "enrollment", Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []FieldMap{
		{Destination: "employee.full_name", Source: "Name"},
		{Destination: "employee.source_id", Source: "Worker"},
		{Destination: "employee.dob", Source: "DOB"},
		{Destination: "employee.ssn", Source: "SSN"},
		{Destination: "enrollment.status", Source: "Status", Literal: "MEDICAL"},
		{Destination: "enrollment.effective_start", Source: "Start", Literal: "MEDICAL"},
		{Destination: "enrollment.effective_end", Source: "End", Literal: "MEDICAL"},
	}}
	logs := StartLogService()
	defer logs.Stop()
	s := &server{db: db, extractor: fixedPlanExtractor{plan: plan}, logs: logs, logDir: t.TempDir()}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /run", s.handleRun)
	mux.HandleFunc("GET /state", s.handleState)
	csv := "Name,Worker,DOB,SSN,Status,Start,End\nTaylor Sample,0042,1985-04-03,999-00-0042,ENROLLED,2026-09-01,2026-01-01\n"
	response := runCSVRequest(t, mux, initial.CompanyName, "valid.csv", csv)
	if response.Code != http.StatusOK {
		t.Fatalf("POST /run(valid plan) = %d %s; want 200", response.Code, response.Body.String())
	}
	var success runResponse
	if err := json.Unmarshal(response.Body.Bytes(), &success); err != nil {
		t.Fatal(err)
	}
	if want := reconcile.Summarize(success.State); !reflect.DeepEqual(success.Summary, want) {
		t.Fatalf("POST /run summary = %#v; want committed-state totals %#v", success.Summary, want)
	}
	if len(success.State.Employees) != 1 || len(success.State.Employees[0].SourceRefs) != 1 || success.State.Employees[0].SourceRefs[0] != (SourceRef{Namespace: "system:test-payroll", Value: "0042"}) {
		t.Fatalf("POST /run(source_system) = %#v; want persisted namespaced identifier with leading zeros", success.State.Employees)
	}
	stateResponse := httptest.NewRecorder()
	mux.ServeHTTP(stateResponse, httptest.NewRequest(http.MethodGet, "/state?company="+url.QueryEscape(initial.CompanyName), nil))
	if stateResponse.Code != http.StatusOK {
		t.Fatalf("GET /state = %d %s; want 200", stateResponse.Code, stateResponse.Body.String())
	}
	var exposed State
	if err := json.Unmarshal(stateResponse.Body.Bytes(), &exposed); err != nil {
		t.Fatal(err)
	}
	stored, err := db.Load(ctx, initial.CompanyName)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(success.State, stored) || !reflect.DeepEqual(exposed, stored) {
		t.Fatalf("POST state = %#v, GET state = %#v; want stored %#v", success.State, exposed, stored)
	}
	if len(stored.Employees) != 1 || len(stored.Enrollments) != 0 {
		t.Fatalf("POST /run(inverted coverage) state = %#v; want one employee and no enrollment", stored)
	}
	var rejected []OpenIssue
	for _, issue := range stored.OpenIssues {
		if issue.Kind == "INVALID_COVERAGE_DATES" {
			rejected = append(rejected, issue)
		}
	}
	if len(rejected) != 1 {
		t.Fatalf("POST /run(inverted coverage) issues = %#v; want one rejected coverage claim", stored.OpenIssues)
	}
	if rejected[0].Key == "" || rejected[0].Field != "coverage_dates" || !reflect.DeepEqual(rejected[0].Claims, []string{"2026-09-01", "2026-01-01"}) {
		t.Fatalf("persisted coverage claim = %#v; want stable key, coverage_dates, and original dates", rejected[0])
	}
	invalid := plan
	invalid.Mappings = append([]FieldMap(nil), plan.Mappings[:2]...)
	invalid.Mappings = append(invalid.Mappings, plan.Mappings[3:]...)
	s.extractor = fixedPlanExtractor{plan: invalid}
	failedResponse := runCSVRequest(t, mux, initial.CompanyName, "missing-identity.csv", csv)
	if failedResponse.Code != http.StatusInternalServerError {
		t.Fatalf("POST /run(omitted SSN mapping) = %d %s; want 500", failedResponse.Code, failedResponse.Body.String())
	}
	var failure errorResponse
	if err := json.Unmarshal(failedResponse.Body.Bytes(), &failure); err != nil {
		t.Fatal(err)
	}
	if failure.RunID == "" || failure.LogPath == "" || !strings.Contains(failure.Error, "identity column") {
		t.Fatalf("POST /run(omitted SSN) = %#v; want extraction error, run_id, and log_path", failure)
	}
	after, err := db.Load(ctx, initial.CompanyName)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, stored) {
		t.Fatalf("Load after invalid extraction = %#v; want unchanged %#v", after, stored)
	}
	logs.Stop()
	writeLog, err := os.ReadFile(filepath.Join(success.LogPath, stepFile("WRITE")))
	if err != nil {
		t.Fatal(err)
	}
	jsonStart := bytes.IndexByte(writeLog, '{')
	if jsonStart < 0 {
		t.Fatalf("WRITE log = %s; want committed state JSON", writeLog)
	}
	var logged State
	if err := json.Unmarshal(writeLog[jsonStart:], &logged); err != nil {
		t.Fatalf("WRITE log JSON: %v; log = %s", err, writeLog)
	}
	if !reflect.DeepEqual(logged, stored) {
		t.Fatalf("WRITE log state = %#v; want stored %#v", logged, stored)
	}
	failureLog, err := os.ReadFile(filepath.Join(failure.LogPath, stepFile("EXTRACT")))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(failureLog, []byte("== EXTRACT failed ==")) {
		t.Fatalf("failed EXTRACT log = %s; want failed stage", failureLog)
	}
	if _, err := os.Stat(filepath.Join(failure.LogPath, stepFile("WRITE"))); !os.IsNotExist(err) {
		t.Fatalf("failed extraction WRITE log error = %v; want no write stage", err)
	}
	for _, tc := range []struct {
		name        string
		blockedStep string
		committed   bool
	}{
		{name: "write-start log failure preserves state", blockedStep: "WRITE"},
		{name: "latency log failure reports committed state", blockedStep: "LATENCY", committed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := StartLogService()
			defer logs.Stop()
			run, err := logs.NewRun(s.logDir, initial.CompanyName, "log-failure.csv")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(run.Path(), stepFile(tc.blockedStep)), 0o755); err != nil {
				t.Fatal(err)
			}
			changedCSV := csv + "Jordan New,0043,1986-04-03,999-00-0043,ENROLLED,2026-09-01,2026-01-01\n"
			result, err := Run(ctx, initial.CompanyName, Document{Filename: "log-failure.csv", Body: []byte(changedCSV)}, db, fixedPlanExtractor{plan: plan}, run)
			if err == nil {
				t.Fatal("Run(unwritable stage log) succeeded; want logging failure")
			}
			after, loadErr := db.Load(ctx, initial.CompanyName)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if tc.committed {
				if !strings.Contains(err.Error(), "state committed") || !reflect.DeepEqual(result, after) || reflect.DeepEqual(after, stored) {
					t.Fatalf("Run(post-commit log failure) = %#v, %v; want changed committed state %#v and explicit error", result, err, after)
				}
			} else if result.CompanyID != "" || !reflect.DeepEqual(after, stored) {
				t.Fatalf("Run(pre-commit log failure) = %#v, saved %#v; want empty result and unchanged %#v", result, after, stored)
			}
		})
	}

}

/*
runCSVRequest sends one synthetic multipart CSV through the actual HTTP route.

* @brief: runCSVRequest -> records the response to a local census upload
* @params: t [*testing.T] -> test reporter
* @params: handler [http.Handler] -> actual HTTP route handler
* @params: company [string] -> isolated test company
* @params: filename [string] -> distinct filename used to identify the run
* @params: csv [string] -> synthetic source content
* @returns: response [*httptest.ResponseRecorder] -> recorded HTTP status and JSON body
*/
func runCSVRequest(t *testing.T, handler http.Handler, company, filename, csv string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("company", company); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("source_system", "test-payroll"); err != nil {
		t.Fatal(err)
	}
	file, err := form.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/run", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
