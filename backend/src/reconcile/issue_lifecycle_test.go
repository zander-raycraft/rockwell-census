package reconcile

import (
	"encoding/json"
	"reflect"
	"testing"
)

/*
TestExplicitRoleResolvesRetainedClaim checks Sofia's live unknown-role claim and retained benefits.

* @brief: TestExplicitRoleResolvesRetainedClaim -> binds a correction without dropping held facts
* @params: t [*testing.T] -> test reporter
*/
func TestExplicitRoleResolvesRetainedClaim(t *testing.T) {
	for _, role := range []string{"dependent", "employee"} {
		t.Run(role, func(t *testing.T) {
			held := Proposed{FirstName: "Sofia", LastName: "Ruiz", DOB: strPtr("2018-03-03"), Enrollments: []Enrollment{{EnrollmentType: "MEDICAL", Status: strPtr("ENROLLED")}}}
			initial := State{CompanyID: "company", Employees: []Employee{{ID: "parent", FirstName: "Ana", LastName: "Lopez", DOB: strPtr("1988-12-01"), SSN: strPtr("912-82-0091")}}}
			if role == "employee" {
				held.EmploymentStatus = boolPointer(true)
			}
			initial.OpenIssues = []OpenIssue{PersonReviewIssue("company", "AMBIGUOUS_ROW_ROLE", held), {ID: "unrelated", Kind: "UNAPPROVED_CHANGE", Status: "OPEN", Subject: "other"}}
			current := Proposed{Role: role, FirstName: held.FirstName, LastName: held.LastName, DOB: held.DOB, Subscriber: "Ana Lopez", Relationship: strPtr("CHILD")}
			if role == "employee" {
				current.Subscriber = ""
				current.Relationship = nil
			}
			formatted := Formatted{People: []Proposed{current}}
			got, _ := Reconcile(initial, formatted, Plan{Intent: "ENROLLMENT"})
			if got.OpenIssues[0].Status != "RESOLVED" || got.OpenIssues[1].Status != "OPEN" || len(got.Enrollments) != 1 {
				t.Fatalf("Reconcile(%s correction) = %#v; want resolved role, unrelated issue open, retained coverage", role, got)
			}
			if role == "employee" && (got.Employees[1].EmploymentStatus == nil || !*got.Employees[1].EmploymentStatus) {
				t.Fatalf("Reconcile(employee correction) lost held employment: %#v", got.Employees)
			}
			replay, _ := Reconcile(got, formatted, Plan{Intent: "ENROLLMENT"})
			if !reflect.DeepEqual(replay, got) {
				t.Fatalf("Reconcile(%s replay) changed state", role)
			}
		})
	}
}

/*
TestRoleCorrectionPreservesAmbiguousOrDeferredClaims rejects household ambiguity and unrelated evidence.

* @brief: TestRoleCorrectionPreservesAmbiguousOrDeferredClaims -> retains facts without a unique supported binding
* @params: t [*testing.T] -> test reporter
*/
func TestRoleCorrectionPreservesAmbiguousOrDeferredClaims(t *testing.T) {
	for _, scenario := range []string{"same name household", "employee shares identity", "wrong parent", "dependent employment", "contradictory held role", "no explicit correction"} {
		t.Run(scenario, func(t *testing.T) {
			held := Proposed{FirstName: "Sofia", LastName: "Ruiz", DOB: strPtr("2018-03-03")}
			state := State{CompanyID: "company", Employees: []Employee{{ID: "parent", FirstName: "Ana", LastName: "Lopez", DOB: strPtr("1988-12-01")}, {ID: "other", FirstName: "Other", LastName: "Parent", DOB: strPtr("1980-01-01")}}, Dependents: []Dependent{{ID: "child", EmployeeID: "parent", FirstName: "Sofia", LastName: "Ruiz", DOB: held.DOB}}}
			current := Proposed{Role: "dependent", FirstName: held.FirstName, LastName: held.LastName, DOB: held.DOB, Subscriber: "Ana Lopez", Relationship: strPtr("CHILD")}
			switch scenario {
			case "same name household":
				state.Dependents = append(state.Dependents, Dependent{ID: "other-child", EmployeeID: "other", FirstName: "Sofia", LastName: "Ruiz", DOB: held.DOB})
			case "employee shares identity":
				state.Employees = append(state.Employees, Employee{ID: "adult", FirstName: "Sofia", LastName: "Ruiz", DOB: held.DOB})
			case "wrong parent":
				held.Subscriber = "Other Parent"
			case "dependent employment":
				held.EmploymentStatus = boolPointer(true)
			case "contradictory held role":
				held.Role = "employee"
			}
			state.OpenIssues = []OpenIssue{PersonReviewIssue("company", "AMBIGUOUS_ROW_ROLE", held)}
			people := []Proposed{current}
			if scenario == "no explicit correction" {
				people = nil
			}
			got, _ := Reconcile(state, Formatted{People: people}, Plan{Intent: "ENROLLMENT"})
			if got.OpenIssues[0].Status != "OPEN" {
				t.Fatalf("Reconcile(%s) resolved unsupported role claim: %#v", scenario, got.OpenIssues[0])
			}
		})
	}
}

/*
TestGenericDependentHasOneStructuredWarning checks the live Casey relationship control.

* @brief: TestGenericDependentHasOneStructuredWarning -> resolves missing subtype without duplicate warnings
* @params: t [*testing.T] -> test reporter
*/
func TestGenericDependentHasOneStructuredWarning(t *testing.T) {
	state := State{CompanyID: "company", Employees: []Employee{{ID: "parent", FirstName: "Taylor", LastName: "Audit", DOB: strPtr("1980-05-12"), SSN: strPtr("900-21-1111")}}}
	plan := Plan{Intent: "ENROLLMENT", Mappings: []FieldMap{{Source: "Name", Destination: "dependent.full_name"}, {Source: "DOB", Destination: "dependent.dob"}, {Source: "Subscriber", Destination: "employee.full_name"}, {Source: "Role", Destination: "row.role"}}}
	row := formatRow(plan, []string{"Name", "DOB", "Subscriber", "Role"}, []string{"Casey Audit", "2010-06-15", "Taylor Audit", "Dependent"}, 1, "company", "ISO")
	if row.Proposed == nil {
		t.Fatal("Format(generic Dependent) did not retain member")
	}
	first, _ := Reconcile(state, Formatted{People: []Proposed{*row.Proposed}, Issues: row.Issues}, plan)
	missing := 0
	for _, issue := range first.OpenIssues {
		if issue.Kind == "UNKNOWN_RELATIONSHIP" {
			t.Fatalf("Reconcile(generic Dependent) duplicated warning: %#v", issue)
		}
		if issue.Kind == "MISSING_FIELD" && issue.Field == "relationship" {
			missing++
		}
	}
	if missing != 1 {
		t.Fatalf("Reconcile(generic Dependent) missing relationship count=%d; want 1", missing)
	}
	corrected := *row.Proposed
	corrected.Relationship = strPtr("CHILD")
	next, _ := Reconcile(first, Formatted{People: []Proposed{corrected}}, plan)
	for _, issue := range next.OpenIssues {
		if issue.Kind == "MISSING_FIELD" && issue.Field == "relationship" && issue.Status != "RESOLVED" {
			t.Fatalf("Reconcile(known relationship) kept warning: %#v", issue)
		}
	}
	row = formatRow(plan, []string{"Name", "DOB", "Subscriber", "Role"}, []string{"Casey Audit", "2010-06-15", "Taylor Audit", "UNRECOGNIZED"}, 1, "company", "ISO")
	retained := false
	for _, issue := range row.Issues {
		retained = retained || issue.Kind == "UNKNOWN_RELATIONSHIP"
	}
	if !retained {
		t.Fatal("Format(unrecognized relationship) discarded supplied code")
	}
	// Historical text-only warnings do not contain enough evidence for automatic binding.
	raw, _ := json.Marshal(Proposed{Role: "dependent", FirstName: "Casey", LastName: "Audit"})
	legacy := OpenIssue{ID: "historical", Kind: "UNKNOWN_RELATIONSHIP", Status: "OPEN", Claims: []string{"dependent relationship unspecified"}}
	next.OpenIssues = append(next.OpenIssues, legacy, OpenIssue{ID: "other-claim", Kind: "UNAPPROVED_CHANGE", Status: "OPEN", Claims: []string{string(raw)}})
	replay, _ := Reconcile(next, Formatted{People: []Proposed{corrected}}, plan)
	if !reflect.DeepEqual(replay, next) {
		t.Fatal("Reconcile(replay) changed unrelated historical warnings")
	}
}

/*
boolPointer provides an optional employment value for reconciliation requirements.

* @brief: boolPointer -> returns a pointer to a test employment value
* @params: value [bool] -> known employment flag
* @returns: result [*bool] -> optional flag
*/
func boolPointer(value bool) *bool { return &value }

/*
TestRejectedClassificationDoesNotResolveRole checks conflicting source evidence on a current row.

* @brief: TestRejectedClassificationDoesNotResolveRole -> requires an accepted correction before applying held facts
* @params: t [*testing.T] -> test reporter
*/
func TestRejectedClassificationDoesNotResolveRole(t *testing.T) {
	held := Proposed{FirstName: "Sofia", LastName: "Ruiz", DOB: strPtr("1980-03-03"), SSN: strPtr("900-11-1111"), Enrollments: []Enrollment{{EnrollmentType: "MEDICAL", Status: strPtr("ENROLLED")}}}
	state := State{CompanyID: "company", Employees: []Employee{{ID: "person", FirstName: held.FirstName, LastName: held.LastName, DOB: held.DOB, SSN: held.SSN}, {ID: "other", FirstName: "Other", LastName: "Person", DOB: strPtr("1970-01-01"), SSN: strPtr("900-22-2222"), SourceRefs: []SourceRef{{Namespace: "prefix:emp-", Value: "EMP-2"}}}}, OpenIssues: []OpenIssue{PersonReviewIssue("company", "AMBIGUOUS_ROW_ROLE", held)}}
	current := held
	current.Role = "employee"
	current.SourceID = "EMP-2"
	current.SourceNamespace = "prefix:emp-"
	current.Enrollments = nil
	got, _ := Reconcile(state, Formatted{People: []Proposed{current}}, Plan{})
	if got.OpenIssues[0].Status != "OPEN" || len(got.Enrollments) != 0 {
		t.Fatalf("Reconcile(rejected classification) = %#v; want held claim open and coverage unapplied", got)
	}
}
