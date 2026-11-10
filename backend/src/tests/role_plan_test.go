package tests

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"census/src/extractor"
	"census/src/parse"
	"census/src/reconcile"
)

/*
TestArchivedRolePlans preserves actual R9 role decisions without guessing conflicting identifiers.

* @brief: TestArchivedRolePlans -> checks captured blank-role and rejected synthetic plans
* @params: t [*testing.T] -> test reporter
*/
func TestArchivedRolePlans(t *testing.T) {
	data, err := os.ReadFile("testdata/r9-role-plans.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Tag    string
		Plan   extractor.Plan
		Parsed parse.Parsed
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	formatted := map[string]reconcile.Formatted{}
	for _, tc := range cases {
		plan := reconcile.NormalizePlan(tc.Plan, tc.Parsed)
		err := reconcile.ValidatePlan(plan, tc.Parsed)
		switch tc.Tag {
		case "tally-missing", "tally-supplied", "tally-replay":
			if err == nil || !strings.Contains(err.Error(), "conflicting sources for employee.source_id") {
				t.Errorf("ValidatePlan(%s) = %v; want conflicting identity sources rejected", tc.Tag, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ValidatePlan(%s) = %v; want accepted", tc.Tag, err)
		}
		got, err := reconcile.Format(plan, tc.Parsed, "company")
		if err != nil {
			t.Fatal(err)
		}
		formatted[tc.Tag] = got
	}
	if !reflect.DeepEqual(formatted["t1-C-1"].People, formatted["t2-C-1"].People) {
		t.Fatal("Format(C repeated plans) produced different people")
	}
	for _, tag := range []string{"t1-C-1", "t2-C-1"} {
		var held []reconcile.Proposed
		for _, issue := range formatted[tag].Issues {
			if issue.Kind == "AMBIGUOUS_ROW_ROLE" {
				var person reconcile.Proposed
				if err := json.Unmarshal([]byte(issue.Claims[0]), &person); err != nil {
					t.Fatal(err)
				}
				held = append(held, person)
			}
		}
		dob := "2020-12-14"
		want := []reconcile.Proposed{{FirstName: "Eli", LastName: "D'Angelo", DOB: &dob, Enrollments: []reconcile.Enrollment{}}}
		if !reflect.DeepEqual(held, want) {
			t.Errorf("Format(%s) held = %#v; want %#v", tag, held, want)
		}
	}
	for _, tc := range []struct {
		tag          string
		relationship string
	}{{"tally-names-missing", "missing"}, {"tally-names-supplied", "CHILD"}, {"tally-names-replay", "CHILD"}} {
		people := formatted[tc.tag].People
		if len(people) != 2 {
			t.Fatalf("Format(%s) people = %#v; want two people", tc.tag, people)
		}
		got := []string{people[0].Role, people[1].Role, people[1].Subscriber, reconcile.StringValue(people[1].Relationship)}
		want := []string{"employee", "dependent", "Taylor Audit", tc.relationship}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Format(%s) roles/link/relationship = %v; want %v", tc.tag, got, want)
		}
	}
}

/*
TestComplementaryRoleMappings holds conflicting roles and retains subtype evidence regardless of order.

* @brief: TestComplementaryRoleMappings -> safely combines separate role and relationship columns
* @params: t [*testing.T] -> test reporter
*/
func TestComplementaryRoleMappings(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "Role", "Relationship"}, Rows: [][]string{{"Casey Audit", "Dependent", "Child"}, {"Taylor Audit", "Employee", "Child"}}}
	plan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{{Destination: "dependent.full_name", Source: "Name"}, {Destination: "row.role", Source: "Role"}, {Destination: "row.role", Source: "Relationship"}}}
	plan = reconcile.NormalizePlan(plan, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		t.Fatal(err)
	}
	got, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	plan.Mappings[1], plan.Mappings[2] = plan.Mappings[2], plan.Mappings[1]
	reversed, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.People, reversed.People) {
		t.Fatal("Format(complementary roles) depends on mapping order")
	}
	if len(got.People) != 1 || got.People[0].Role != "dependent" || reconcile.StringValue(got.People[0].Relationship) != "CHILD" {
		t.Fatalf("Format(complementary roles) people = %#v; want one CHILD dependent", got.People)
	}
	for _, result := range []reconcile.Formatted{got, reversed} {
		held := 0
		for _, issue := range result.Issues {
			if issue.Kind == "AMBIGUOUS_ROW_ROLE" {
				held++
			}
		}
		if held != 1 {
			t.Errorf("Format(conflicting roles) held = %d; want 1", held)
		}
	}
}

/*
TestBlankRoleRetainsProposedEvidence holds identity, employment, and coverage without assigning a role.

* @brief: TestBlankRoleRetainsProposedEvidence -> prevents benefit or hire fields from guessing a blank role
* @params: t [*testing.T] -> test reporter
*/
func TestBlankRoleRetainsProposedEvidence(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "Role", "DOB", "Hired", "Medical"}, Rows: [][]string{{"Casey Audit", "", "2010-06-15", "2026-01-01", "ENROLLED"}}}
	plan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{{Destination: "dependent.full_name", Source: "Name"}, {Destination: "row.role", Source: "Role"}, {Destination: "dependent.dob", Source: "DOB"}, {Destination: "employee.hire_date", Source: "Hired"}, {Destination: "enrollment.status", Source: "Medical", Literal: "MEDICAL"}}}
	formatted, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	if len(formatted.People) != 0 || len(formatted.Issues) != 1 || formatted.Issues[0].Kind != "AMBIGUOUS_ROW_ROLE" {
		t.Fatalf("Format(blank role) = %#v; want held row", formatted)
	}
	var got reconcile.Proposed
	if err := json.Unmarshal([]byte(formatted.Issues[0].Claims[0]), &got); err != nil {
		t.Fatal(err)
	}
	dob, hire, status := "2010-06-15", "2026-01-01", "ENROLLED"
	want := reconcile.Proposed{FirstName: "Casey", LastName: "Audit", DOB: &dob, HireDate: &hire, Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", Status: &status}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Format(blank role) held = %#v; want %#v", got, want)
	}
	state, _ := reconcile.Reconcile(reconcile.State{CompanyID: "company"}, formatted, plan)
	if len(state.Employees) != 0 || len(state.Dependents) != 0 || len(state.Enrollments) != 0 {
		t.Fatalf("Reconcile(blank role) saved guessed member: %#v", state)
	}
}
