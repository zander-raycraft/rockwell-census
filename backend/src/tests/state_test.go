package tests

import (
	"census/src/parse"
	"census/src/reconcile"
	"context"
	"os"
	"reflect"
	"testing"
)

/*
TestNormalizeCensusPreservesInvalidCoverageClaim keeps rejected dates visible without mutating input.

* @brief: TestNormalizeCensusPreservesInvalidCoverageClaim -> locks rejected-claim behavior
* @params: t [*testing.T] -> test reporter
*/
func TestNormalizeCensusPreservesInvalidCoverageClaim(t *testing.T) {
	start, end := "2026-09-01", "2026-01-01"
	input := reconcile.State{
		CompanyID: "company", Dependents: []reconcile.Dependent{}, OpenIssues: []reconcile.OpenIssue{},
		Employees: []reconcile.Employee{{ID: "employee", SourceRefs: []reconcile.SourceRef{{Namespace: "payroll", Value: "42"}}}},
		Enrollments: []reconcile.Enrollment{{ID: "coverage", EmployeeID: "employee", EnrollmentType: "MEDICAL",
			EffectiveStart: &start, EffectiveEnd: &end}},
	}

	before := reconcile.CopyState(input)
	got, err := reconcile.NormalizeCensus(input)
	if err != nil {
		t.Fatalf("normalizeCensus(inverted dates): %v", err)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatalf("normalizeCensus mutated input: got %#v, want %#v", input, before)
	}
	if len(got.Enrollments) != 0 || len(got.OpenIssues) != 1 {
		t.Fatalf("normalizeCensus(inverted dates) = %#v; want no enrollment and one issue", got)
	}
	issue := got.OpenIssues[0]
	want := reconcile.OpenIssue{
		ID: issue.ID, CompanyID: "company", EmployeeID: reconcile.CloneString(&input.Employees[0].ID),
		Kind: "INVALID_COVERAGE_DATES", Status: "OPEN",
		Key:   "INVALID_COVERAGE_DATES|employee|employee|MEDICAL|2026-09-01|2026-01-01",
		Field: "coverage_dates", Subject: "employee", Claims: []string{start, end},
		Detail: "MEDICAL coverage end 2026-01-01 precedes start 2026-09-01",
	}

	if !reflect.DeepEqual(issue, want) {
		t.Fatalf("normalizeCensus issue = %#v, want %#v", issue, want)
	}
	repeated := reconcile.CopyState(got)
	repeated.Enrollments = input.Enrollments
	again, err := reconcile.NormalizeCensus(repeated)
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Fatalf("normalizeCensus(repeated rejected claim) = %#v, %v; want %#v", again, err, got)
	}
	got.Employees[0].SourceRefs[0].Value = "changed"
	got.OpenIssues[0].Claims[0] = "changed"
	if !reflect.DeepEqual(input, before) {
		t.Fatalf("normalized state aliases input: got %#v, want %#v", input, before)
	}
}

/*
TestNormalizeCensusRejectsInvalidLinks prevents malformed relationships from silently disappearing.

* @brief: TestNormalizeCensusRejectsInvalidLinks -> rejects missing or inconsistent parents
* @params: t [*testing.T] -> test reporter
*/
func TestNormalizeCensusRejectsInvalidLinks(t *testing.T) {
	missing, dependent := "missing", "dependent"
	cases := []struct {
		name  string
		state reconcile.State
	}{
		{"dependent missing owner", reconcile.State{Dependents: []reconcile.Dependent{{ID: "dependent"}}}},
		{"dependent unknown owner", reconcile.State{Dependents: []reconcile.Dependent{{ID: "dependent", EmployeeID: "missing"}}}},
		{"enrollment unknown owner", reconcile.State{Enrollments: []reconcile.Enrollment{{ID: "coverage", EmployeeID: "missing"}}}},
		{"enrollment unknown dependent", reconcile.State{Employees: []reconcile.Employee{{ID: "employee"}}, Enrollments: []reconcile.Enrollment{{ID: "coverage", EmployeeID: "employee", DependentID: &missing}}}},
		{"enrollment wrong household", reconcile.State{Employees: []reconcile.Employee{{ID: "employee"}, {ID: "other"}}, Dependents: []reconcile.Dependent{{ID: "dependent", EmployeeID: "other"}}, Enrollments: []reconcile.Enrollment{{ID: "coverage", EmployeeID: "employee", DependentID: &dependent}}}},
		{"issue unknown employee", reconcile.State{OpenIssues: []reconcile.OpenIssue{{ID: "issue", EmployeeID: &missing}}}},
		{"source without namespace", reconcile.State{Employees: []reconcile.Employee{{ID: "employee", SourceRefs: []reconcile.SourceRef{{Value: "42"}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := reconcile.NormalizeCensus(tc.state); err == nil {
				t.Fatalf("normalizeCensus(%s) = %#v,nil; want invalid-link error", tc.name, got)
			}
		})
	}
}

/*
TestSaveLoadCommittedState verifies the real transaction result and rollback on an isolated database.

* @brief: TestSaveLoadCommittedState -> checks persisted state agrees with Save and failed writes roll back
* @params: t [*testing.T] -> test reporter and optional database configuration
*/
func TestSaveLoadCommittedState(t *testing.T) {
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
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	suffix, err := reconcile.CensusUUID()
	if err != nil {
		t.Fatal(err)
	}
	input, err := db.Load(ctx, "storage-test-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.RemoveCompany(ctx, input.CompanyID); err != nil {
			t.Error(err)
		}
	}()
	first, err := reconcile.CensusUUID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := reconcile.CensusUUID()
	if err != nil {
		t.Fatal(err)
	}
	start, end := "2026-09-01", "2026-01-01"
	input.Employees = []reconcile.Employee{{ID: first, FirstName: "Test", LastName: "One", SourceRefs: []reconcile.SourceRef{{Namespace: "payroll", Value: "42"}}}, {ID: second, FirstName: "Test", LastName: "Two"}}
	input.Enrollments = []reconcile.Enrollment{{EmployeeID: first, EnrollmentType: "MEDICAL", EffectiveStart: &start, EffectiveEnd: &end}}
	committed, err := db.Save(ctx, input, parse.Document{})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := db.Load(ctx, input.CompanyName)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(committed, loaded) {
		t.Fatalf("Save then Load = %#v, want committed %#v", loaded, committed)
	}
	invalid := reconcile.CopyState(committed)
	invalid.Employees[0].FirstName = "must roll back"
	invalid.OpenIssues = append(invalid.OpenIssues, reconcile.OpenIssue{Kind: "TEST", Status: "INVALID"})
	if got, err := db.Save(ctx, invalid, parse.Document{}); err == nil {
		t.Fatalf("Save(invalid status) = %#v,nil; want transaction error", got)
	}
	after, err := db.Load(ctx, input.CompanyName)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, committed) {
		t.Fatalf("Load after failed Save = %#v, want %#v", after, committed)
	}
}
