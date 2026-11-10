package tests

import (
	"census/src/extractor"
	"census/src/reconcile"
	"encoding/json"
	"reflect"
	"testing"
)

/*
TestUndatedConflictingCoverageIsHeld retains an uncertain election rather than accepting another period.

* @brief: TestUndatedConflictingCoverageIsHeld -> checks retained claims, replay, and dated elections
* @params: t [*testing.T] -> test runner
*/
func TestUndatedConflictingCoverageIsHeld(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "employee", FirstName: "Taylor", LastName: "Walker", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}}, Enrollments: []reconcile.Enrollment{{ID: "election", EmployeeID: "employee", EnrollmentType: "MEDICAL", Plan: strPtr("PPO"), EffectiveStart: strPtr("2026-01-01")}}}
	person := reconcile.Proposed{Role: "employee", FirstName: "Taylor", LastName: "Walker", SSN: strPtr("900-11-1111"), Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", Plan: strPtr("HMO")}}}
	plan := extractor.Plan{Intent: "SNAPSHOT"}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{person}}, plan)
	if !reflect.DeepEqual(next.Enrollments, state.Enrollments) || len(next.OpenIssues) != 1 {
		t.Fatalf("Reconcile(undated HMO) = %+v; want unchanged PPO and one retained review issue", next)
	}
	issue := next.OpenIssues[0]
	var claim reconcile.Enrollment
	if len(issue.Claims) != 1 {
		t.Fatalf("Reconcile(undated HMO) claims = %v; want one election claim", issue.Claims)
	}
	if err := json.Unmarshal([]byte(issue.Claims[0]), &claim); err != nil {
		t.Fatal(err)
	}
	want := person.Enrollments[0]
	want.EmployeeID = "employee"
	if issue.Kind != "AMBIGUOUS_COVERAGE_PERIOD" || issue.Subject != "employee" || issue.Status != "OPEN" || !reflect.DeepEqual(claim, want) {
		t.Fatalf("Reconcile(undated HMO) issue = %+v, claim = %+v; want open ambiguity with claim %+v", issue, claim, want)
	}
	replay, _ := reconcile.Reconcile(next, reconcile.Formatted{People: []reconcile.Proposed{person}}, plan)
	if !reflect.DeepEqual(replay, next) {
		t.Fatalf("Reconcile(undated HMO replay) = %+v; want %+v", replay, next)
	}
	person.Enrollments[0].EffectiveStart = strPtr("2027-01-01")
	dated, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{person}}, plan)
	if len(dated.Enrollments) != 2 || len(dated.OpenIssues) != 0 {
		t.Fatalf("Reconcile(new dated HMO) = %+v; want two elections without ambiguity", dated)
	}
	state.Enrollments = nil
	person.Enrollments[0].EffectiveStart = nil
	first, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{person}}, plan)
	if len(first.Enrollments) != 1 || len(first.OpenIssues) != 0 {
		t.Fatalf("Reconcile(first undated HMO) = %+v; want one accepted election", first)
	}
}

/*
TestMergeRemapsDependentAndEnrollmentIssues keeps review subjects attached to surviving records.

* @brief: TestMergeRemapsDependentAndEnrollmentIssues -> checks relationship resolution and coverage review after merging
* @params: t [*testing.T] -> test runner
*/
func TestMergeRemapsDependentAndEnrollmentIssues(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{
		{ID: "a", FirstName: "Bob", LastName: "Moses", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")},
		{ID: "b", FirstName: "Robert", LastName: "Moses", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")},
	}, Dependents: []reconcile.Dependent{
		{ID: "d1", EmployeeID: "a", FirstName: "Casey", LastName: "Moses", DOB: strPtr("2010-01-01"), Relationship: strPtr("CHILD")},
		{ID: "d2", EmployeeID: "b", FirstName: "Casey", LastName: "Moses", DOB: strPtr("2010-01-01")},
	}, Enrollments: []reconcile.Enrollment{
		{ID: "e1", EmployeeID: "a", DependentID: strPtr("d1"), EnrollmentType: "MEDICAL", CoverageTier: strPtr("EMPLOYEE_SPOUSE_ONLY"), Status: strPtr("ENROLLED")},
		{ID: "e2", EmployeeID: "b", DependentID: strPtr("d2"), EnrollmentType: "MEDICAL", CoverageTier: strPtr("EMPLOYEE_SPOUSE_ONLY"), Status: strPtr("ENROLLED")},
	}, OpenIssues: []reconcile.OpenIssue{{ID: "unrelated", CompanyID: "company", EmployeeID: strPtr("a"), Subject: "d1", Kind: "CONFLICT_DOB", Field: "dob", Claims: []string{"2010-01-01", "2011-01-01"}, Status: "OPEN"}}}
	state, _ = reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "SNAPSHOT"})
	// The second child's coverage issue can predate the document that lost its relationship.
	state.OpenIssues = append(state.OpenIssues, reconcile.OpenIssue{ID: "coverage2", CompanyID: "company", EmployeeID: strPtr("b"), Subject: "e2", Kind: "COVERAGE_RELATIONSHIP_CONFLICT", Field: "coverage_tier", Claims: []string{"CHILD", "EMPLOYEE_SPOUSE_ONLY"}, Status: "OPEN"})
	plan := extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: []string{"Bob Moses", "Robert Moses"}, SamePerson: true}}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{}, plan)
	if len(next.Dependents) != 1 || len(next.Enrollments) != 1 {
		t.Fatalf("Reconcile(confirmed merge) = %+v; want one surviving child and election", next)
	}
	missing, coverage, unrelated := 0, 0, 0
	for _, issue := range next.OpenIssues {
		if issue.Subject == "d2" || issue.Subject == "e2" {
			t.Fatalf("Reconcile(confirmed merge) issue = %+v; subject references a removed record", issue)
		}
		switch issue.Kind {
		case "MISSING_FIELD":
			missing++
			if issue.Subject != "d1" || issue.Status != "RESOLVED" || issue.Key == "" {
				t.Fatalf("Reconcile(confirmed merge) missing issue = %+v; want resolved relationship on d1", issue)
			}
		case "COVERAGE_RELATIONSHIP_CONFLICT":
			coverage++
			if issue.Subject != "e1" || issue.Status != "OPEN" {
				t.Fatalf("Reconcile(confirmed merge) coverage issue = %+v; want open conflict on e1", issue)
			}
		case "CONFLICT_DOB":
			unrelated++
			if !reflect.DeepEqual(issue, state.OpenIssues[0]) {
				t.Fatalf("Reconcile(confirmed merge) unrelated issue = %+v; want %+v", issue, state.OpenIssues[0])
			}
		}
	}
	if missing != 1 || coverage != 1 || unrelated != 1 {
		t.Fatalf("Reconcile(confirmed merge) issue counts = %d/%d/%d; want one relationship, coverage, and unrelated issue", missing, coverage, unrelated)
	}
	replay, _ := reconcile.Reconcile(next, reconcile.Formatted{}, plan)
	if !reflect.DeepEqual(replay, next) {
		t.Fatalf("Reconcile(confirmed merge replay) = %+v; want %+v", replay, next)
	}
}

/*
TestExplicitSSNCorrectionResolvesMergeConflict targets SSN conflicts without closing unrelated claims.

* @brief: TestExplicitSSNCorrectionResolvesMergeConflict -> checks retained SSNs and targeted correction replay
* @params: t [*testing.T] -> test runner
*/
func TestExplicitSSNCorrectionResolvesMergeConflict(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{
		{ID: "a", FirstName: "Bob", LastName: "Moses", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")},
		{ID: "b", FirstName: "Robert", LastName: "Moses", SSN: strPtr("900-22-2222")},
	}, OpenIssues: []reconcile.OpenIssue{{ID: "unrelated", CompanyID: "company", EmployeeID: strPtr("a"), Subject: "a", Kind: "CONFLICT_DOB", Field: "dob", Claims: []string{"1980-01-01", "1981-01-01"}, Status: "OPEN"}}}
	plan := extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: []string{"Bob Moses", "Robert Moses"}, SamePerson: true}}
	merged, _ := reconcile.Reconcile(state, reconcile.Formatted{}, plan)
	if len(merged.Employees) != 1 || len(merged.OpenIssues) != 3 {
		t.Fatalf("Reconcile(SSN merge) = %+v; want one employee with SSN conflict and confirmation", merged)
	}
	conflict := merged.OpenIssues[1]
	if conflict.Kind != "CONFLICT_SSN" || conflict.Subject != "a" || conflict.Field != "ssn" || conflict.Status != "OPEN" || !reflect.DeepEqual(conflict.Claims, []string{"900-11-1111", "900-22-2222"}) {
		t.Fatalf("Reconcile(SSN merge) conflict = %+v; want employee-bound open SSN claims", conflict)
	}
	plan.EmailResolution.SSN = "900-33-3333"
	plan.EmailResolution.CorrectSSN = true
	corrected, _ := reconcile.Reconcile(merged, reconcile.Formatted{}, plan)
	if !reflect.DeepEqual(corrected.OpenIssues[0], state.OpenIssues[0]) || corrected.OpenIssues[1].Status != "RESOLVED" || *corrected.Employees[0].SSN != "900-33-3333" {
		t.Fatalf("Reconcile(explicit SSN correction) = %+v; want corrected SSN, resolved SSN issue, unchanged DOB issue", corrected)
	}
	replay, _ := reconcile.Reconcile(corrected, reconcile.Formatted{}, plan)
	if !reflect.DeepEqual(replay, corrected) {
		t.Fatalf("Reconcile(SSN correction replay) = %+v; want %+v", replay, corrected)
	}
}
