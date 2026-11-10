package tests

import (
	"census/src/extractor"
	"census/src/reconcile"
	"reflect"
	"testing"
)

/*
TestDocumentPolicyPreservesConfirmedState holds unapproved changes and ignores no-change messages.

* @brief: TestDocumentPolicyPreservesConfirmedState -> keeps requests out of confirmed census facts
* @params: t [*testing.T] -> test runner
*/
func TestDocumentPolicyPreservesConfirmedState(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "employee", FirstName: "Robin", LastName: "Walker"}}}
	person := reconcile.Proposed{Role: "employee", FirstName: "Robin", LastName: "Walker", SourceID: "K-001", Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", Plan: strPtr("Requested")}}}
	formatted := reconcile.Formatted{People: []reconcile.Proposed{person, person}}
	next, _ := reconcile.Reconcile(state, formatted, extractor.Plan{Intent: "NO_CHANGE", Uncertainties: []string{"No facts"}})
	if !reflect.DeepEqual(next, reconcile.CopyState(state)) {
		t.Fatalf("Reconcile(no change) = %#v; want %#v", next, reconcile.CopyState(state))
	}
	next, _ = reconcile.Reconcile(state, formatted, extractor.Plan{Intent: "CHANGE_REQUEST"})
	if !reflect.DeepEqual(next.Employees, state.Employees) || len(next.Enrollments) != 0 || len(next.OpenIssues) != 1 || next.OpenIssues[0].Kind != "UNAPPROVED_CHANGE" {
		t.Fatalf("Reconcile(request) = %#v; want original employee, no coverage, one review issue", next)
	}
	again, _ := reconcile.Reconcile(next, formatted, extractor.Plan{Intent: "CHANGE_REQUEST"})
	if !reflect.DeepEqual(again, next) {
		t.Errorf("Reconcile(repeated request) = %#v; want %#v", again, next)
	}
}

/*
TestReconcileDefersUncertainIdentity keeps conflicting SSNs and insufficient evidence out of automatic merges.

* @brief: TestReconcileDefersUncertainIdentity -> preserves unconfirmed incoming facts in review claims
* @params: t [*testing.T] -> test runner
*/
func TestReconcileDefersUncertainIdentity(t *testing.T) {
	original := reconcile.Employee{ID: "employee", FirstName: "Jordan", LastName: "Ellis", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{original}}
	incoming := reconcile.Proposed{Role: "employee", FirstName: "Jordan", LastName: "Ellis", DOB: strPtr("1980-01-01"), SSN: strPtr("900-22-2222")}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{incoming}}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(next.Employees, state.Employees) || len(next.OpenIssues) != 1 || next.OpenIssues[0].Kind != "AMBIGUOUS_MATCH" || len(next.OpenIssues[0].Claims) != 1 {
		t.Fatalf("Reconcile(conflicting identities) = %#v; want original identity and preserved review claim", next)
	}
	next, _ = reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{{Role: "employee", FirstName: "Alex", LastName: "Morgan"}}}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(next.Employees) != 1 || next.OpenIssues[0].Kind != "INSUFFICIENT_IDENTITY" {
		t.Errorf("Reconcile(name only) = %#v; want no new employee and identity review", next)
	}
}

/*
TestSourceReferencesSurviveDocuments reuses confirmed namespace links independently of subscriber spelling.

* @brief: TestSourceReferencesSurviveDocuments -> links later household records from persisted employee references
* @params: t [*testing.T] -> test runner
*/
func TestSourceReferencesSurviveDocuments(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "employee", FirstName: "Robert", LastName: "Walker", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}}}
	person := reconcile.Proposed{Role: "employee", SourceID: "K-BEN-0001", FirstName: "Bob", LastName: "Walker", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}
	linked, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{person}}, extractor.Plan{Intent: "SNAPSHOT"})
	child := reconcile.Proposed{Role: "dependent", SourceID: "K-BEN-0001", Subscriber: "Bob Walker", FirstName: "Hana", LastName: "Park", DOB: strPtr("2010-01-01"), Relationship: strPtr("CHILD")}
	next, _ := reconcile.Reconcile(reconcile.CopyState(linked), reconcile.Formatted{People: []reconcile.Proposed{child}}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(next.Dependents) != 1 || next.Dependents[0].EmployeeID != "employee" {
		t.Fatalf("Reconcile(later household) = %#v; want child under original employee", next)
	}
	if len(state.Employees[0].SourceRefs) != 0 {
		t.Fatal("Reconcile modified input employee references")
	}
	other, _ := reconcile.Reconcile(reconcile.State{CompanyID: "other"}, reconcile.Formatted{People: []reconcile.Proposed{child}}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(other.Dependents) != 0 {
		t.Errorf("Reconcile(other company) = %#v; want unresolved household", other)
	}
	if reconcile.SourceReference("0001").Namespace != "" {
		t.Fatal("sourceReference(numeric ID) guessed a durable namespace")
	}
}

/*
TestMissingIssuesResolveByField closes a missing DOB only after evidence fills that DOB.

* @brief: TestMissingIssuesResolveByField -> maintains stable issues across replay and later evidence
* @params: t [*testing.T] -> test runner
*/
func TestMissingIssuesResolveByField(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "employee", FirstName: "Maya", LastName: "Morgan", SSN: strPtr("900-11-1111")}}}
	missing, _ := reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "SNAPSHOT"})
	replay, _ := reconcile.Reconcile(missing, reconcile.Formatted{}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(replay, missing) {
		t.Fatalf("Reconcile(replayed missing field) = %#v; want %#v", replay, missing)
	}
	filled, _ := reconcile.Reconcile(missing, reconcile.Formatted{People: []reconcile.Proposed{{Role: "employee", FirstName: "Maya", LastName: "Morgan", SSN: strPtr("900-11-1111"), DOB: strPtr("1980-01-01")}}}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(filled.OpenIssues) != 1 || filled.OpenIssues[0].ID != missing.OpenIssues[0].ID || filled.OpenIssues[0].Status != "RESOLVED" {
		t.Errorf("Reconcile(DOB supplied) = %#v; want original issue resolved", filled)
	}
}

/*
TestEmailCorrectionTargetsSSN merges source links and resolves SSN claims without resolving DOB conflicts.

* @brief: TestEmailCorrectionTargetsSSN -> applies explicit field correction with narrow issue resolution
* @params: t [*testing.T] -> test runner
*/
func TestEmailCorrectionTargetsSSN(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{
		{ID: "a", FirstName: "Bob", LastName: "Moses", SSN: strPtr("900-11-1111"), SourceRefs: []reconcile.SourceRef{{Namespace: "system:payroll", Value: "001"}}},
		{ID: "b", FirstName: "Robert", LastName: "Moses", SSN: strPtr("900-22-2222"), SourceRefs: []reconcile.SourceRef{{Namespace: "system:benefits", Value: "001"}}},
	}, OpenIssues: []reconcile.OpenIssue{{ID: "ssn-issue", EmployeeID: strPtr("b"), Subject: "b", Kind: "CONFLICT_SSN", Field: "ssn", Status: "OPEN"}, {ID: "dob-issue", EmployeeID: strPtr("b"), Subject: "b", Kind: "CONFLICT_DOB", Field: "dob", Status: "OPEN"}}}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: []string{"Bob Moses", "Robert Moses"}, SamePerson: true, SSN: "900-33-3333", CorrectSSN: true}})
	if len(next.Employees) != 1 || reconcile.StringValue(next.Employees[0].SSN) != "900-33-3333" || len(next.Employees[0].SourceRefs) != 2 {
		t.Fatalf("Reconcile(explicit correction) = %#v; want merged identity, two refs, corrected SSN", next)
	}
	for _, issue := range next.OpenIssues {
		if issue.ID == "ssn-issue" && issue.Status != "RESOLVED" {
			t.Error("Reconcile(explicit SSN correction) left SSN conflict open")
		}
		if issue.ID == "dob-issue" && issue.Status != "OPEN" {
			t.Error("Reconcile(explicit SSN correction) resolved unrelated DOB conflict")
		}
	}
}

/*
TestHRConfirmationAppliesHeldCoverage applies a deferred nickname claim only after explicit confirmation.

* @brief: TestHRConfirmationAppliesHeldCoverage -> preserves review evidence until HR confirms a match
* @params: t [*testing.T] -> test runner
*/
func TestHRConfirmationAppliesHeldCoverage(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "employee", FirstName: "Robert", LastName: "Moses", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}}}
	held, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{{Role: "employee", FirstName: "Bob", LastName: "Moses", DOB: strPtr("1980-01-01"), SourceID: "BEN-001", Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", Status: strPtr("ENROLLED")}}}}}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(held.Employees) != 1 || len(held.Enrollments) != 0 || len(held.OpenIssues) != 1 {
		t.Fatalf("Reconcile(unconfirmed nickname) = %#v; want held coverage and one existing employee", held)
	}
	confirmed, _ := reconcile.Reconcile(held, reconcile.Formatted{}, extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: []string{"Bob Moses", "Robert Moses"}, SamePerson: true}})
	if len(confirmed.Employees) != 1 || len(confirmed.Enrollments) != 1 || len(confirmed.Employees[0].SourceRefs) != 1 || confirmed.OpenIssues[0].Status != "RESOLVED" {
		t.Errorf("Reconcile(HR confirmation) = %#v; want held coverage linked and identity issue resolved", confirmed)
	}
}

/*
TestDistinctPeopleCanShareAName retains two people supported by different dates and identifiers.

* @brief: TestDistinctPeopleCanShareAName -> prevents an identity guard from rejecting distinct same-name people
* @params: t [*testing.T] -> test runner
*/
func TestDistinctPeopleCanShareAName(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "employee", FirstName: "Jamie", LastName: "Walker", DOB: strPtr("1985-04-03"), SSN: strPtr("902-80-1003")}}}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{{Role: "employee", FirstName: "Jamie", LastName: "Walker", DOB: strPtr("1994-08-22"), SSN: strPtr("902-80-1004")}}}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(next.Employees) != 2 || len(next.OpenIssues) != 0 {
		t.Errorf("Reconcile(distinct same-name people) = %#v; want two employees without identity review", next)
	}
}

/*
TestHRConfirmationDoesNotSelectBetweenHeldNames preserves ambiguous claims even when their names match an email.

* @brief: TestHRConfirmationDoesNotSelectBetweenHeldNames -> requires an unambiguous held identity
* @params: t [*testing.T] -> test runner
*/
func TestHRConfirmationDoesNotSelectBetweenHeldNames(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "employee", FirstName: "Robert", LastName: "Moses", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}}}
	decisions := []string{}
	for _, dob := range []string{"1980-01-01", "1990-01-01"} {
		reconcile.AddIssue(&state, &decisions, reconcile.PersonReviewIssue("company", "POSSIBLE_MATCH", reconcile.Proposed{Role: "employee", FirstName: "Bob", LastName: "Moses", DOB: strPtr(dob), Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", Status: strPtr("ENROLLED")}}}))
	}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: []string{"Bob Moses", "Robert Moses"}, SamePerson: true}})
	if len(next.Enrollments) != 0 {
		t.Fatalf("Reconcile(ambiguous held names) = %#v; want no coverage applied", next.Enrollments)
	}
	for _, issue := range next.OpenIssues {
		if issue.Kind == "POSSIBLE_MATCH" && issue.Status != "OPEN" {
			t.Error("Reconcile(ambiguous held names) resolved an unselected claim")
		}
	}
}

/*
TestCorrectionCannotTakeAnotherEmployeesSSN defers an explicit correction that conflicts with another identity.

* @brief: TestCorrectionCannotTakeAnotherEmployeesSSN -> prevents duplicate SSNs from a correction
* @params: t [*testing.T] -> test runner
*/
func TestCorrectionCannotTakeAnotherEmployeesSSN(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "a", FirstName: "Robert", LastName: "Moses", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}, {ID: "b", FirstName: "Alice", LastName: "Jones", DOB: strPtr("1990-01-01"), SSN: strPtr("900-22-2222")}}}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: []string{"Bob Moses", "Robert Moses"}, SamePerson: true, CorrectSSN: true, SSN: "900-22-2222"}})
	if !reflect.DeepEqual(next.Employees, state.Employees) {
		t.Errorf("Reconcile(SSN owned by other employee) = %#v; want %#v", next.Employees, state.Employees)
	}
	found := false
	for _, issue := range next.OpenIssues {
		found = found || (issue.Kind == "CONFLICT_SSN" && issue.Status == "OPEN")
	}
	if !found {
		t.Error("Reconcile(SSN owned by other employee) did not retain correction conflict")
	}
}
