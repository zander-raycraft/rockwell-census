package tests

import (
	"reflect"
	"strings"
	"testing"

	"census/src/extractor"
	"census/src/parse"
	"census/src/reconcile"
)

/*
TestSeparateSubscriberReference applies the actual R10 identifier control without losing its parent link.

* @brief: TestSeparateSubscriberReference -> checks separate worker and subscriber columns
* @params: t [*testing.T] -> test reporter
*/
func TestSeparateSubscriberReference(t *testing.T) {
	body := "First Name,Last Name,DOB,SSN,Role,Relationship,Employee ID,Subscriber ID\n" +
		"Taylor,Audit,1980-05-12,900-21-1111,Employee,,AUD001,\n" +
		"Casey,Audit,2010-06-15,,Dependent,,,AUD001\n"
	plan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{
		{Destination: "employee.first_name", Source: "First Name"},
		{Destination: "employee.last_name", Source: "Last Name"},
		{Destination: "dependent.dob", Source: "DOB"},
		{Destination: "dependent.ssn", Source: "SSN"},
		{Destination: "row.role", Source: "Role"},
		{Destination: "dependent.relationship", Source: "Relationship"},
		{Destination: "employee.source_id", Source: "Employee ID"},
		{Destination: "dependent.subscriber_id", Source: "Subscriber ID"},
	}}
	state := reconcile.State{CompanyID: "company"}
	fixed := strings.Replace(body, "Dependent,,,", "Dependent,Child,,", 1)
	for i, input := range []string{body, fixed, fixed} {
		parsed, err := parse.Parse(parse.Document{Filename: "relationship-control.csv", Body: []byte(input)})
		if err != nil {
			t.Fatal(err)
		}
		plan = reconcile.NormalizePlan(plan, parsed)
		if err := reconcile.ValidatePlan(plan, parsed); err != nil {
			t.Fatalf("ValidatePlan(R10 separate references) = %v; want accepted", err)
		}
		formatted, err := reconcile.Format(plan, parsed, "company")
		if err != nil {
			t.Fatal(err)
		}
		next, _ := reconcile.Reconcile(state, formatted, plan)
		if i == 2 && !reflect.DeepEqual(next, state) {
			t.Errorf("Reconcile(R10 replay) = %#v; want %#v", next, state)
		}
		state = next
		if len(state.Employees) != 1 || len(state.Dependents) != 1 || state.Dependents[0].EmployeeID != state.Employees[0].ID {
			t.Fatalf("Reconcile(R10 separate references) = %#v; want Casey linked to Taylor", state)
		}
	}
	if got := reconcile.StringValue(state.Dependents[0].Relationship); got != "CHILD" {
		t.Errorf("Reconcile(corrected R10) relationship = %q; want CHILD", got)
	}
}

/*
TestSubscriberReferenceKeepsDistinctSystems does not mistake a dependent's own ID for the subscriber.

* @brief: TestSubscriberReferenceKeepsDistinctSystems -> preserves distinct and conflicting references
* @params: t [*testing.T] -> test reporter
*/
func TestSubscriberReferenceKeepsDistinctSystems(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "Role", "Employee ID", "Subscriber ID"}, Rows: [][]string{{"Casey Audit", "Child", "CRM001", "AUD001"}}}
	plan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{
		{Destination: "dependent.full_name", Source: "Name"}, {Destination: "row.role", Source: "Role"},
		{Destination: "employee.source_id", Source: "Employee ID"}, {Destination: "dependent.subscriber_id", Source: "Subscriber ID"},
	}}
	plan = reconcile.NormalizePlan(plan, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		t.Fatal(err)
	}
	formatted, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	initial := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{
		{ID: "parent", FirstName: "Taylor", LastName: "Audit", SourceRefs: []reconcile.SourceRef{reconcile.SourceReference("AUD001")}},
		{ID: "other", FirstName: "Other", LastName: "Person", SourceRefs: []reconcile.SourceRef{reconcile.SourceReference("CRM001")}},
	}}
	got, _ := reconcile.Reconcile(initial, formatted, plan)
	if len(got.Dependents) != 1 || got.Dependents[0].EmployeeID != "parent" {
		t.Fatalf("Reconcile(distinct references) dependents = %#v; want Casey under parent", got.Dependents)
	}
	replay, _ := reconcile.Reconcile(got, formatted, plan)
	if !reflect.DeepEqual(replay, got) {
		t.Errorf("Reconcile(distinct reference replay) = %#v; want %#v", replay, got)
	}
	initial.Employees[1].SourceRefs = []reconcile.SourceRef{reconcile.SourceReference("AUD001")}
	conflicted, _ := reconcile.Reconcile(initial, formatted, plan)
	if len(conflicted.Dependents) != 0 {
		t.Fatalf("Reconcile(conflicting parent references) dependents = %#v; want held", conflicted.Dependents)
	}
	for _, issue := range conflicted.OpenIssues {
		if issue.Kind == "UNMATCHED_DEPENDENT" && issue.Status == "OPEN" {
			return
		}
	}
	t.Errorf("Reconcile(conflicting parent references) issues = %#v; want open UNMATCHED_DEPENDENT", conflicted.OpenIssues)
}

/*
TestSubscriberReferenceDisagreesWithName holds contradictory parent evidence rather than choosing an owner.

* @brief: TestSubscriberReferenceDisagreesWithName -> requires a unique parent reference and name to agree
* @params: t [*testing.T] -> test reporter
*/
func TestSubscriberReferenceDisagreesWithName(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Subscriber Name", "Member Name", "Relationship", "Subscriber ID"}, Rows: [][]string{{"Taylor Audit", "Casey Audit", "Child", "CRM001"}}}
	plan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{
		{Destination: "employee.full_name", Source: "Subscriber Name"},
		{Destination: "dependent.full_name", Source: "Member Name"},
		{Destination: "dependent.relationship", Source: "Relationship"},
		{Destination: "dependent.subscriber_id", Source: "Subscriber ID"},
	}}
	plan = reconcile.NormalizePlan(plan, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		t.Fatal(err)
	}
	formatted, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	initial := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{
		{ID: "named", FirstName: "Taylor", LastName: "Audit", SourceRefs: []reconcile.SourceRef{reconcile.SourceReference("AUD001")}},
		{ID: "referenced", FirstName: "Other", LastName: "Person", SourceRefs: []reconcile.SourceRef{reconcile.SourceReference("CRM001")}},
	}}
	got, _ := reconcile.Reconcile(initial, formatted, plan)
	if len(got.Dependents) != 0 {
		t.Fatalf("Reconcile(disagreeing parent name/reference) dependents = %#v; want held", got.Dependents)
	}
	for _, issue := range got.OpenIssues {
		if issue.Kind == "UNMATCHED_DEPENDENT" && issue.Status == "OPEN" {
			return
		}
	}
	t.Errorf("Reconcile(disagreeing parent name/reference) issues = %#v; want open UNMATCHED_DEPENDENT", got.OpenIssues)
}

/*
TestConfirmedSubscriberAliases uses accepted F identity rows to disambiguate a third literal Alex Morgan.

* @brief: TestConfirmedSubscriberAliases -> preserves proven reference/name pairs regardless of row order
* @params: t [*testing.T] -> test reporter
*/
func TestConfirmedSubscriberAliases(t *testing.T) {
	ssn1, ssn2, ssn3 := "903-40-0001", "903-40-0002", "903-40-0017"
	dob1, dob2 := "1965-01-01", "1966-02-02"
	initial := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{
		{ID: "alexander", FirstName: "Alexander", LastName: "Morgan", DOB: &dob1, SSN: &ssn1, SourceRefs: []reconcile.SourceRef{reconcile.SourceReference("F-BEN-0001")}},
		{ID: "alexandra", FirstName: "Alexandra", LastName: "Morgan", DOB: &dob2, SSN: &ssn2, SourceRefs: []reconcile.SourceRef{reconcile.SourceReference("F-BEN-0002")}},
		{ID: "alex", FirstName: "Alex", LastName: "Morgan", SSN: &ssn3},
	}}
	dependents := []reconcile.Proposed{
		{Role: "dependent", SourceID: "F-BEN-0001", Subscriber: "Alex Morgan", FirstName: "Priya", LastName: "Walker"},
		{Role: "dependent", SourceID: "F-BEN-0002", Subscriber: "Alex Morgan", FirstName: "Miles", LastName: "Morgan"},
	}
	selfRows := []reconcile.Proposed{
		{Role: "employee", SourceID: "F-BEN-0001", FirstName: "Alex", LastName: "Morgan", DOB: &dob1, SSN: &ssn1},
		{Role: "employee", SourceID: "F-BEN-0002", FirstName: "Alex", LastName: "Morgan", DOB: &dob2, SSN: &ssn2},
	}
	plan := extractor.Plan{Intent: "SNAPSHOT"}
	for _, people := range [][]reconcile.Proposed{append(append([]reconcile.Proposed{}, dependents...), selfRows...), append(append([]reconcile.Proposed{}, selfRows...), dependents...)} {
		got, _ := reconcile.Reconcile(initial, reconcile.Formatted{People: people}, plan)
		owners := map[string]string{}
		for _, dependent := range got.Dependents {
			owners[dependent.FirstName] = dependent.EmployeeID
		}
		want := map[string]string{"Priya": "alexander", "Miles": "alexandra"}
		if !reflect.DeepEqual(owners, want) {
			t.Errorf("Reconcile(confirmed F aliases) owners = %v; want %v", owners, want)
		}
	}
	held, _ := reconcile.Reconcile(initial, reconcile.Formatted{People: dependents}, plan)
	if len(held.Dependents) != 0 {
		t.Fatalf("Reconcile(unconfirmed F aliases) dependents = %#v; want held", held.Dependents)
	}
	recovered, _ := reconcile.Reconcile(held, reconcile.Formatted{People: selfRows}, plan)
	if len(recovered.Dependents) != 2 {
		t.Errorf("Reconcile(held F aliases plus confirming self rows) dependents = %#v; want two recovered", recovered.Dependents)
	}
	rejected := selfRows[0]
	rejected.SSN = &ssn3
	got, _ := reconcile.Reconcile(initial, reconcile.Formatted{People: append([]reconcile.Proposed{rejected}, dependents...)}, plan)
	if len(got.Dependents) != 0 {
		t.Errorf("Reconcile(rejected F self row) dependents = %#v; want held", got.Dependents)
	}
}
