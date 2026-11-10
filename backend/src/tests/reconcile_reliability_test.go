package tests

import (
	"census/src/extractor"
	"census/src/parse"
	"census/src/reconcile"
	"os"
	"reflect"
	"testing"
)

/*
TestCompanyBPreservesFactsUntilIdentityConfirmation exercises both source files and later HR replies.

* @brief: TestCompanyBPreservesFactsUntilIdentityConfirmation -> checks complete reconciliation and replay states
* @params: t [*testing.T] -> test runner
*/
func TestCompanyBPreservesFactsUntilIdentityConfirmation(t *testing.T) {
	payroll, err := os.ReadFile("../../../examples/company_b_payroll.csv")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := os.ReadFile("../../../examples/company_b_enrollment.csv")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := parse.Parse(parse.Document{Filename: "payroll.csv", Body: payroll})
	e, _ := parse.Parse(parse.Document{Filename: "enrollment.csv", Body: enrollment})
	payrollPlan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{
		{Destination: "employee.first_name", Source: "Legal First Name"},
		{Destination: "employee.last_name", Source: "Legal Last Name"},
		{Destination: "employee.dob", Source: "Date of Birth"},
		{Destination: "employee.ssn", Source: "SSN"},
		{Destination: "employee.hire_date", Source: "Hire Date"},
	}}
	enrollmentPlan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "MDY", Mappings: []extractor.FieldMap{
		{Destination: "employee.source_id", Source: "Employee ID"},
		{Destination: "employee.full_name", Source: "Employee Name"},
		{Destination: "dependent.full_name", Source: "Member Name"},
		{Destination: "dependent.dob", Source: "Member DOB"},
		{Destination: "row.role", Source: "Relationship"},
		{Destination: "dependent.relationship", Source: "Relationship"},
		{Destination: "enrollment.type", Source: "Coverage Type"},
		{Destination: "enrollment.plan", Source: "Plan Name"},
		{Destination: "enrollment.coverage_tier", Source: "Coverage Level"},
		{Destination: "enrollment.effective_start", Source: "Effective Date"},
		{Destination: "enrollment.status", Source: "Enrollment Status"},
	}}
	formattedPayroll, err := reconcile.Format(payrollPlan, p, "company")
	if err != nil {
		t.Fatal(err)
	}
	formattedEnrollment, err := reconcile.Format(enrollmentPlan, e, "company")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := reconcile.Reconcile(reconcile.State{CompanyID: "company"}, formattedPayroll, payrollPlan)
	state, _ = reconcile.Reconcile(state, formattedEnrollment, enrollmentPlan)
	if len(state.Employees) != 10 || len(state.Dependents) != 3 || len(state.Enrollments) != 14 {
		t.Fatalf("Reconcile(B files) counts = %d/%d/%d; want 10 employees, 3 dependents, 14 confirmed elections with unresolved aliases held", len(state.Employees), len(state.Dependents), len(state.Enrollments))
	}
	for _, names := range [][]string{{"Bob Thompson", "Robert Thompson"}, {"Kate Nguyen", "Katherine Nguyen"}} {
		state, _ = reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: names, SamePerson: true}})
	}
	if len(state.Employees) != 10 || len(state.Dependents) != 5 || len(state.Enrollments) != 22 {
		t.Fatalf("Reconcile(B confirmations) counts = %d/%d/%d; want 10/5/22 with Samuel's contradictory DOB still held", len(state.Employees), len(state.Dependents), len(state.Enrollments))
	}
	for _, dependent := range state.Dependents {
		parent := reconcile.EmployeeByID(state.Employees, dependent.EmployeeID)
		want := map[string]string{"Linda": "Robert Thompson", "Lucas": "Maria Garcia-Lopez", "Daniel": "Christine Lee", "Mia": "Christine Lee", "Ethan": "Katherine Nguyen"}[dependent.FirstName]
		if parent != want || dependent.Relationship == nil {
			t.Errorf("Reconcile(B dependent %s) parent/relationship = %s/%v; want %s and explicit relationship", dependent.FirstName, parent, dependent.Relationship, want)
		}
	}
	for _, employee := range state.Employees {
		for _, coverage := range state.Enrollments {
			if coverage.EmployeeID == employee.ID && employee.FirstName == "Derek" && (reconcile.StringValue(coverage.Status) != "TERMINATED" || reconcile.StringValue(coverage.EffectiveEnd) != "2026-09-30") {
				t.Errorf("Reconcile(Derek) = %#v; want explicit termination and end date", coverage)
			}
		}
	}
	replayed, _ := reconcile.Reconcile(state, formattedEnrollment, enrollmentPlan)
	if !reflect.DeepEqual(replayed, state) {
		t.Errorf("Reconcile(B replay) changed state; before counts %d/%d/%d/%d, after %d/%d/%d/%d", len(state.Employees), len(state.Dependents), len(state.Enrollments), len(state.OpenIssues), len(replayed.Employees), len(replayed.Dependents), len(replayed.Enrollments), len(replayed.OpenIssues))
	}
}

/*
TestCorroboratedNamesStayAmbiguous protects nearby spelling candidates and known conflicting identifiers.

* @brief: TestCorroboratedNamesStayAmbiguous -> requires unique DOB-backed name evidence
* @params: t [*testing.T] -> test runner
*/
func TestCorroboratedNamesStayAmbiguous(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{
		{ID: "a", FirstName: "Christopher", LastName: "Lee", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")},
		{ID: "b", FirstName: "Christine", LastName: "Lee", DOB: strPtr("1980-01-01"), SSN: strPtr("900-22-2222")},
	}}
	person := reconcile.Proposed{Role: "employee", FirstName: "Chris", LastName: "Lee", DOB: strPtr("1980-01-01"), Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", Status: strPtr("ENROLLED")}}}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{person}}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(next.Employees, state.Employees) || len(next.Enrollments) != 0 || len(next.OpenIssues) != 1 || len(next.OpenIssues[0].Claims) != 1 {
		t.Fatalf("Reconcile(two Chris candidates) = %#v; want unchanged identities and preserved claim", next)
	}
	state.Employees = state.Employees[:1]
	person.SSN = strPtr("900-33-3333")
	next, _ = reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{person}}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(next.Employees, state.Employees) || len(next.Enrollments) != 0 {
		t.Errorf("Reconcile(prefix with conflicting SSN) = %#v; want held facts", next)
	}
}

/*
TestCoveragePeriodFulfillmentIsIdempotent fills a known period and preserves ambiguity between periods.

* @brief: TestCoveragePeriodFulfillmentIsIdempotent -> avoids duplicate elections and arbitrary undated updates
* @params: t [*testing.T] -> test runner
*/
func TestCoveragePeriodFulfillmentIsIdempotent(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "a", FirstName: "Robin", LastName: "Walker", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}}, Enrollments: []reconcile.Enrollment{{ID: "coverage", EmployeeID: "a", EnrollmentType: "MEDICAL", EffectiveStart: strPtr("2026-01-01"), Status: strPtr("ENROLLED")}}}
	person := reconcile.Proposed{Role: "employee", FirstName: "Robin", LastName: "Walker", SSN: strPtr("900-11-1111"), Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", EffectiveStart: strPtr("2026-01-01"), EffectiveEnd: strPtr("2026-09-30"), Status: strPtr("TERMINATED")}}}
	next, _ := reconcile.Reconcile(state, reconcile.Formatted{People: []reconcile.Proposed{person}}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(next.Enrollments) != 1 || next.Enrollments[0].ID != "coverage" || reconcile.StringValue(next.Enrollments[0].EffectiveEnd) != "2026-09-30" || reconcile.StringValue(next.Enrollments[0].Status) != "ENROLLED" {
		t.Fatalf("Reconcile(period fulfillment) = %#v; want same row, end filled, conflicting status preserved", next)
	}
	again, _ := reconcile.Reconcile(next, reconcile.Formatted{People: []reconcile.Proposed{person}}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(again, next) {
		t.Errorf("Reconcile(period replay) = %#v; want %#v", again, next)
	}
	next.Enrollments = append(next.Enrollments, reconcile.Enrollment{ID: "future", EmployeeID: "a", EnrollmentType: "MEDICAL", EffectiveStart: strPtr("2027-01-01"), Status: strPtr("ENROLLED")})
	person.Enrollments = []reconcile.Enrollment{{EnrollmentType: "MEDICAL", Plan: strPtr("Unknown period")}}
	ambiguous, _ := reconcile.Reconcile(next, reconcile.Formatted{People: []reconcile.Proposed{person}}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(ambiguous.Enrollments, next.Enrollments) {
		t.Errorf("Reconcile(undated coverage) = %#v; want periods unchanged", ambiguous.Enrollments)
	}
	found := false
	for _, issue := range ambiguous.OpenIssues {
		found = found || issue.Kind == "AMBIGUOUS_COVERAGE_PERIOD" && len(issue.Claims) == 1
	}
	if !found {
		t.Error("Reconcile(undated coverage) did not preserve the ambiguous claim")
	}
}

/*
TestPartialIdentityAndDependentFulfillment fills missing facts while retaining unrelated family conflicts.

* @brief: TestPartialIdentityAndDependentFulfillment -> verifies narrow issue resolution and household replay
* @params: t [*testing.T] -> test runner
*/
func TestPartialIdentityAndDependentFulfillment(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "a", FirstName: "Robin", LastName: "Walker"}}, Dependents: []reconcile.Dependent{{ID: "child", EmployeeID: "a", FirstName: "Hana", LastName: "Park", Relationship: strPtr("CHILD")}}}
	missing, _ := reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "SNAPSHOT"})
	people := []reconcile.Proposed{
		{Role: "employee", FirstName: "Robin", LastName: "Walker", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")},
		{Role: "dependent", Subscriber: "Robin Walker", FirstName: "Hana", LastName: "Park", DOB: strPtr("2010-01-01"), SSN: strPtr("900-22-2222"), Relationship: strPtr("CHILD"), Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", Status: strPtr("ENROLLED")}}},
	}
	filled, _ := reconcile.Reconcile(missing, reconcile.Formatted{People: people}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(filled.Employees) != 1 || len(filled.Dependents) != 1 || filled.Dependents[0].ID != "child" || reconcile.StringValue(filled.Dependents[0].DOB) != "2010-01-01" || len(filled.Enrollments) != 1 {
		t.Fatalf("Reconcile(partial family identity) = %#v; want existing identities filled with one coverage", filled)
	}
	for _, issue := range filled.OpenIssues {
		if issue.Kind == "MISSING_FIELD" && issue.Status != "RESOLVED" {
			t.Errorf("Reconcile(supplied identity) left missing issue open: %#v", issue)
		}
	}
	decisions := []string{}
	conflicting := people[1]
	conflicting.Relationship = strPtr("SPOUSE")
	reconcile.FillDependent(&filled.Dependents[0], conflicting, &filled, &decisions)
	childConflict := reconcile.ConflictIssue("company", "a", "CONFLICT_SSN", "Hana Park", "ssn", "900-22-2222", "900-33-3333")
	childConflict.Subject = "child"
	reconcile.AddIssue(&filled, &decisions, childConflict)
	corrected, _ := reconcile.Reconcile(filled, reconcile.Formatted{}, extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: []string{"Robin Walker", "Rob Walker"}, SamePerson: true, CorrectSSN: true, SSN: "900-44-4444"}})
	for _, issue := range corrected.OpenIssues {
		if issue.Subject == "child" && issue.Status != "OPEN" {
			t.Errorf("Reconcile(parent correction) resolved child's conflict: %#v", issue)
		}
	}
	replay, _ := reconcile.Reconcile(corrected, reconcile.Formatted{People: []reconcile.Proposed{people[1]}}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(replay, corrected) {
		t.Errorf("Reconcile(dependent replay) = %#v; want %#v", replay, corrected)
	}
}
