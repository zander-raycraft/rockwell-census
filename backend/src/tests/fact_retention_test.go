package tests

import (
	"census/src/extractor"
	"census/src/parse"
	"census/src/reconcile"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

/*
TestArchivedRoleMappingsRetainRelationships checks the actual B/D plans that omitted relationship mappings.

* @brief: TestArchivedRoleMappingsRetainRelationships -> retains source relationships and household links
* @params: t [*testing.T] -> test reporter
*/
func TestArchivedRoleMappingsRetainRelationships(t *testing.T) {
	for _, tc := range []struct {
		company, file, member, parent, source string
		relationships                         map[string]string
	}{
		{"b", "company_b_enrollment.csv", "Linda Thompson", "Bob Thompson", "BA-1001", map[string]string{"Linda Thompson": "SPOUSE", "Lucas Garcia-Lopez": "CHILD", "Daniel Park": "SPOUSE", "Mia Lee-Park": "CHILD", "Ethan Nguyen": "CHILD"}},
		{"d", "synthetic/company_d_enrollment.csv", "Taylor Bennett", "Liz Bennett", "BEN-41", map[string]string{"Taylor Bennett": "SPOUSE", "Morgan Shah": "SPOUSE", "Nico Patel": "CHILD", "Mila Martinez": "CHILD"}},
	} {
		t.Run(tc.company, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "company_"+tc.company+"_relationship_omitted.json"))
			if err != nil {
				t.Fatal(err)
			}
			var plan extractor.Plan
			if err := json.Unmarshal(data, &plan); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parse.Parse(parse.Document{Filename: tc.file, Body: body})
			if err != nil {
				t.Fatal(err)
			}
			plan = reconcile.NormalizePlan(plan, parsed)
			if err := reconcile.ValidatePlan(plan, parsed); err != nil {
				t.Fatal(err)
			}
			formatted, err := reconcile.Format(plan, parsed, "company")
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, person := range formatted.People {
				if person.Role == "dependent" {
					got[reconcile.DisplayName(person.FirstName, person.LastName)] = reconcile.StringValue(person.Relationship)
				}
			}
			if !reflect.DeepEqual(got, tc.relationships) {
				t.Fatalf("Format(archived %s) relationships = %#v; want %#v", tc.company, got, tc.relationships)
			}
			first, last := reconcile.SplitName(tc.parent)
			initial := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "owner", FirstName: first, LastName: last, SourceRefs: []reconcile.SourceRef{reconcile.SourceReference(tc.source)}}}}
			state, _ := reconcile.Reconcile(initial, formatted, plan)
			linked := false
			for _, dependent := range state.Dependents {
				if reconcile.DisplayName(dependent.FirstName, dependent.LastName) == tc.member {
					linked = dependent.EmployeeID == "owner" && reconcile.StringValue(dependent.Relationship) == "SPOUSE"
				}
			}
			if !linked {
				t.Fatalf("Reconcile(archived %s) failed to link %s as spouse to owner: %#v", tc.company, tc.member, state.Dependents)
			}
			replay, _ := reconcile.Reconcile(state, formatted, plan)
			if !reflect.DeepEqual(replay, state) {
				t.Fatalf("Reconcile(archived %s replay) changed state", tc.company)
			}
		})
	}
}

/*
TestUnmappedFactsRemainReviewable prevents irrelevant mappings from hiding supplied facts.

* @brief: TestUnmappedFactsRemainReviewable -> retains raw omitted facts and holds unclassified rows
* @params: t [*testing.T] -> test reporter
*/
func TestUnmappedFactsRemainReviewable(t *testing.T) {
	for _, tc := range []struct {
		name, header, value, destination, field string
		held                                    bool
	}{
		{"omitted relationship", "Relationship", "Spouse", "", "dependent.relationship", true},
		{"misrouted relationship", "Rel Code", "CH", "enrollment.plan", "dependent.relationship", true},
		{"omitted status", "Enrollment Status", "Active", "", "enrollment.status", false},
		{"misrouted status", "Medical Status", "UNKNOWN-CODE", "enrollment.plan", "enrollment.status", false},
		{"omitted employment", "Worker Status", "Inactive", "", "employee.employment_status", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{{Source: "Name", Destination: "employee.full_name"}, {Source: "DOB", Destination: "employee.dob"}}}
			if tc.destination != "" {
				plan.Mappings = append(plan.Mappings, extractor.FieldMap{Source: tc.header, Destination: tc.destination, Literal: "MEDICAL"})
			}
			parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "DOB", tc.header}, Rows: [][]string{{"Jamie Green", "1980-01-01", tc.value}}}
			formatted, err := reconcile.Format(plan, parsed, "company")
			if err != nil {
				t.Fatal(err)
			}
			if (len(formatted.People) == 0) != tc.held {
				t.Fatalf("Format(%s) people = %#v; held want %v", tc.name, formatted.People, tc.held)
			}
			if len(formatted.Issues) != 1 {
				t.Fatalf("Format(%s) issues = %#v; want one retained fact", tc.name, formatted.Issues)
			}
			issue := formatted.Issues[0]
			if issue.Kind != "UNMAPPED_SOURCE_FIELD" || issue.Field != tc.field || !strings.Contains(strings.Join(issue.Claims, " "), tc.value) || !strings.Contains(strings.Join(issue.Claims, " "), tc.header) {
				t.Fatalf("Format(%s) lost original field: %#v", tc.name, issue)
			}
			first, _ := reconcile.Reconcile(reconcile.State{CompanyID: "company"}, formatted, plan)
			replay, _ := reconcile.Reconcile(first, formatted, plan)
			if !reflect.DeepEqual(replay, first) {
				t.Fatalf("Reconcile(%s replay) changed state", tc.name)
			}
		})
	}
}

/*
TestWideStatusAndUnknownValuesStayExplicit preserves supported wide mappings and unfamiliar codes.

* @brief: TestWideStatusAndUnknownValuesStayExplicit -> distinguishes applied status from unknown meaning
* @params: t [*testing.T] -> test reporter
*/
func TestWideStatusAndUnknownValuesStayExplicit(t *testing.T) {
	for _, tc := range []struct{ name, value, destination, kind string }{
		{"wide waiver", "Waived", "enrollment.coverage_tier", ""},
		{"unknown status", "QZ", "enrollment.status", "UNKNOWN_STATUS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "DOB", "Medical Status"}, Rows: [][]string{{"Jamie Green", "1980-01-01", tc.value}}}
			plan := extractor.Plan{DateFormat: "ISO", Mappings: []extractor.FieldMap{{Source: "Name", Destination: "employee.full_name"}, {Source: "DOB", Destination: "employee.dob"}, {Source: "Medical Status", Destination: tc.destination, Literal: "MEDICAL"}}}
			formatted, err := reconcile.Format(plan, parsed, "company")
			if err != nil {
				t.Fatal(err)
			}
			if tc.kind == "" {
				if len(formatted.Issues) != 0 || reconcile.StringValue(formatted.People[0].Enrollments[0].Status) != "WAIVED" {
					t.Fatalf("Format(wide waiver) = %#v; want WAIVED without omission issue", formatted)
				}
				return
			}
			if len(formatted.Issues) != 1 || formatted.Issues[0].Kind != tc.kind || !strings.Contains(strings.Join(formatted.Issues[0].Claims, " "), tc.value) || len(formatted.People[0].Enrollments) != 0 {
				t.Fatalf("Format(unknown status) = %#v; want retained QZ without invented coverage", formatted)
			}
		})
	}
}

/*
TestCoverageContradictionsPreserveFactsAndResolveNarrowly checks explicit member elections only.

* @brief: TestCoverageContradictionsPreserveFactsAndResolveNarrowly -> flags mismatches without mutating coverage
* @params: t [*testing.T] -> test reporter
*/
func TestCoverageContradictionsPreserveFactsAndResolveNarrowly(t *testing.T) {
	for _, tc := range []struct {
		name, relationship, tier, status string
		conflict                         bool
	}{
		{"child spouse tier", "CHILD", "EMPLOYEE_SPOUSE_ONLY", "ENROLLED", true},
		{"spouse children tier", "SPOUSE", "EMPLOYEE_CHILDREN", "ENROLLED", true},
		{"dependent employee tier", "OTHER", "EMPLOYEE", "ENROLLED", true},
		{"waived", "CHILD", "EMPLOYEE_SPOUSE_ONLY", "WAIVED", false},
		{"unknown status", "CHILD", "EMPLOYEE_SPOUSE_ONLY", "", false},
		{"unknown relationship", "", "EMPLOYEE_SPOUSE_ONLY", "ENROLLED", false},
		{"family compatible", "CHILD", "EMPLOYEE_FAMILY", "ENROLLED", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := reconcile.State{CompanyID: "company", Dependents: []reconcile.Dependent{{ID: "child", EmployeeID: "owner", FirstName: "Jamie", LastName: "Green", Relationship: strPtr(tc.relationship)}}, Enrollments: []reconcile.Enrollment{{ID: "coverage", EmployeeID: "owner", DependentID: strPtr("child"), EnrollmentType: "MEDICAL", CoverageTier: strPtr(tc.tier), Status: strPtr(tc.status)}}, OpenIssues: []reconcile.OpenIssue{{ID: "other", Kind: "CONFLICT", Field: "dob", Status: "OPEN"}}}
			next, _ := reconcile.Reconcile(initial, reconcile.Formatted{}, extractor.Plan{})
			if !reflect.DeepEqual(next.Dependents, initial.Dependents) || !reflect.DeepEqual(next.Enrollments, initial.Enrollments) {
				t.Fatalf("Reconcile(%s) modified supplied facts", tc.name)
			}
			wantCount := 1
			if tc.conflict {
				wantCount++
			}
			if tc.relationship == "" {
				wantCount++
			}
			if len(next.OpenIssues) != wantCount {
				t.Fatalf("Reconcile(%s) issues = %#v; want %d", tc.name, next.OpenIssues, wantCount)
			}
			replay, _ := reconcile.Reconcile(next, reconcile.Formatted{}, extractor.Plan{})
			if !reflect.DeepEqual(replay, next) {
				t.Fatalf("Reconcile(%s replay) changed state", tc.name)
			}
			corrected := reconcile.CopyState(next)
			corrected.Enrollments[0].CoverageTier = strPtr("EMPLOYEE_FAMILY")
			resolved, _ := reconcile.Reconcile(corrected, reconcile.Formatted{}, extractor.Plan{})
			for _, issue := range resolved.OpenIssues {
				if issue.Kind == "COVERAGE_RELATIONSHIP_CONFLICT" && issue.Status != "RESOLVED" {
					t.Fatalf("Reconcile(%s compatible facts) left tier issue open", tc.name)
				}
				if issue.ID == "other" && issue.Status != "OPEN" {
					t.Fatalf("Reconcile(%s compatible facts) resolved unrelated DOB issue", tc.name)
				}
			}
		})
	}
}

/*
TestAmbiguousRolesStayHeld preserves unknown and contradictory source roles for review.

* @brief: TestAmbiguousRolesStayHeld -> prevents ambiguous role evidence from inserting people
* @params: t [*testing.T] -> test reporter
*/
func TestAmbiguousRolesStayHeld(t *testing.T) {
	for _, cells := range [][]string{{"Employee", "Spouse"}, {"Spouse", "Employee"}, {"QZ", ""}} {
		parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "DOB", "Relationship", "Other Role"}, Rows: [][]string{{"Jamie Green", "1980-01-01", cells[0], cells[1]}}}
		plan := extractor.Plan{Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{{Source: "Name", Destination: "employee.full_name"}, {Source: "DOB", Destination: "employee.dob"}, {Source: "Relationship", Destination: "row.role"}, {Source: "Other Role", Destination: "dependent.relationship"}}}
		formatted, err := reconcile.Format(plan, parsed, "company")
		if err != nil {
			t.Fatal(err)
		}
		next, _ := reconcile.Reconcile(reconcile.State{CompanyID: "company"}, formatted, plan)
		if len(next.Employees) != 0 || len(next.Dependents) != 0 || len(next.OpenIssues) == 0 {
			t.Fatalf("Reconcile(roles %v) = %#v; want held row", cells, next)
		}
		retained := false
		for _, issue := range next.OpenIssues {
			retained = retained || strings.Contains(strings.Join(issue.Claims, " "), "Jamie")
		}
		if !retained {
			t.Fatalf("Reconcile(roles %v) lost person facts: %#v", cells, next.OpenIssues)
		}
	}
}

/*
TestHeldChangeRequestPreservesSourceEvidence keeps formatted-only claims through intent handling.

* @brief: TestHeldChangeRequestPreservesSourceEvidence -> retains unclassified requested rows without applying them
* @params: t [*testing.T] -> test reporter
*/
func TestHeldChangeRequestPreservesSourceEvidence(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "DOB", "Relationship"}, Rows: [][]string{{"Jamie Green", "1980-01-01", "Child"}}}
	plan := extractor.Plan{Intent: "CHANGE_REQUEST", DateFormat: "ISO", Mappings: []extractor.FieldMap{{Source: "Name", Destination: "employee.full_name"}, {Source: "DOB", Destination: "employee.dob"}}}
	formatted, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	next, _ := reconcile.Reconcile(reconcile.State{CompanyID: "company"}, formatted, plan)
	if len(next.Employees) != 0 || len(next.Dependents) != 0 || len(next.OpenIssues) != 1 || !strings.Contains(strings.Join(next.OpenIssues[0].Claims, " "), "Jamie") {
		t.Fatalf("Reconcile(unclassified change request) = %#v; want retained row only", next)
	}
}

/*
TestCoverageIssueRequiresKnownCorrection leaves contradictions open when evidence becomes unknown.

* @brief: TestCoverageIssueRequiresKnownCorrection -> resolves only actual coverage corrections
* @params: t [*testing.T] -> test reporter
*/
func TestCoverageIssueRequiresKnownCorrection(t *testing.T) {
	initial := reconcile.State{CompanyID: "company", Dependents: []reconcile.Dependent{{ID: "child", EmployeeID: "owner", Relationship: strPtr("CHILD")}}, Enrollments: []reconcile.Enrollment{{ID: "coverage", EmployeeID: "owner", DependentID: strPtr("child"), EnrollmentType: "MEDICAL", CoverageTier: strPtr("EMPLOYEE_SPOUSE_ONLY"), Status: strPtr("ENROLLED")}}}
	next, _ := reconcile.Reconcile(initial, reconcile.Formatted{}, extractor.Plan{})
	unknown := reconcile.CopyState(next)
	unknown.Dependents[0].Relationship = nil
	unresolved, _ := reconcile.Reconcile(unknown, reconcile.Formatted{}, extractor.Plan{})
	if len(unresolved.OpenIssues) != 2 || unresolved.OpenIssues[0].Status != "OPEN" || unresolved.OpenIssues[1].Kind != "MISSING_FIELD" || unresolved.OpenIssues[1].Status != "OPEN" {
		t.Fatalf("Reconcile(unknown relationship) = %#v; want existing open contradiction", unresolved.OpenIssues)
	}
	waived := reconcile.CopyState(next)
	waived.Enrollments[0].Status = strPtr("WAIVED")
	resolved, _ := reconcile.Reconcile(waived, reconcile.Formatted{}, extractor.Plan{})
	if len(resolved.OpenIssues) != 1 || resolved.OpenIssues[0].Status != "RESOLVED" {
		t.Fatalf("Reconcile(waived correction) = %#v; want resolved contradiction", resolved.OpenIssues)
	}
}

/*
TestGenericDependentRelationshipStaysUnknown checks generic role codes without inventing a subtype.

* @brief: TestGenericDependentRelationshipStaysUnknown -> reviews missing subtype only after all mappings
* @params: t [*testing.T] -> test reporter
*/
func TestGenericDependentRelationshipStaysUnknown(t *testing.T) {
	for _, tc := range []struct {
		relationship string
		issues       int
	}{{"", 0}, {"Spouse", 0}} {
		parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "Role", "Relationship"}, Rows: [][]string{{"Jamie Green", "DEP", tc.relationship}}}
		plan := extractor.Plan{Mappings: []extractor.FieldMap{{Source: "Name", Destination: "dependent.full_name"}, {Source: "Role", Destination: "row.role"}, {Source: "Relationship", Destination: "dependent.relationship"}}}
		formatted, err := reconcile.Format(plan, parsed, "company")
		if err != nil {
			t.Fatal(err)
		}
		if len(formatted.People) != 1 || formatted.People[0].Role != "dependent" || len(formatted.Issues) != tc.issues {
			t.Fatalf("Format(DEP, relationship %q) = %#v; want dependent with %d review issues", tc.relationship, formatted, tc.issues)
		}
		if tc.relationship == "" && formatted.People[0].Relationship != nil {
			t.Fatalf("Format(DEP) invented relationship %v", formatted.People[0].Relationship)
		}
	}
}

/*
TestConflictingRelationshipsRemainUnknown retains both explicit subtype claims without choosing one.

* @brief: TestConflictingRelationshipsRemainUnknown -> keeps dependent role and reviews conflicting subtypes
* @params: t [*testing.T] -> test reporter
*/
func TestConflictingRelationshipsRemainUnknown(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "Relationship", "Other Relationship"}, Rows: [][]string{{"Jamie Green", "Spouse", "Child"}}}
	plan := extractor.Plan{Mappings: []extractor.FieldMap{{Source: "Name", Destination: "dependent.full_name"}, {Source: "Relationship", Destination: "row.role"}, {Source: "Other Relationship", Destination: "dependent.relationship"}}}
	formatted, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	if len(formatted.People) != 1 || formatted.People[0].Role != "dependent" || formatted.People[0].Relationship != nil || len(formatted.Issues) != 1 || formatted.Issues[0].Kind != "CONFLICT" {
		t.Fatalf("Format(Spouse vs Child) = %#v; want unknown dependent relationship with conflict", formatted)
	}
	claims := strings.Join(formatted.Issues[0].Claims, " ")
	if !strings.Contains(strings.ToUpper(claims), "SPOUSE") || !strings.Contains(strings.ToUpper(claims), "CHILD") {
		t.Fatalf("Format(Spouse vs Child) lost claims: %#v", formatted.Issues)
	}
}

/*
TestMissingDependentRelationshipResolvesNarrowly checks later facts fulfill only the matching member's issue.

* @brief: TestMissingDependentRelationshipResolvesNarrowly -> flags blanks, resolves supplied relationships, preserves other reviews
* @params: t [*testing.T] -> test reporter
*/
func TestMissingDependentRelationshipResolvesNarrowly(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "owner", FirstName: "Robin", LastName: "Walker", SSN: strPtr("900-11-1111")}}, Dependents: []reconcile.Dependent{{ID: "child", EmployeeID: "owner", FirstName: "Hana", LastName: "Park"}, {ID: "other", EmployeeID: "owner", FirstName: "Remy", LastName: "Walker"}}}
	missing, _ := reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "SNAPSHOT"})
	if got := reconcile.Summarize(missing); got.MembersMissingRequiredFields != 3 || got.OpenIssues != 3 {
		t.Fatalf("Reconcile(blank dependent relationships) summary = %#v; want three missing members and issues", got)
	}
	person := reconcile.Proposed{Role: "dependent", Subscriber: "Robin Walker", FirstName: "Hana", LastName: "Park", Relationship: strPtr("CHILD")}
	filled, decisions := reconcile.Reconcile(missing, reconcile.Formatted{People: []reconcile.Proposed{person}}, extractor.Plan{Intent: "SNAPSHOT"})
	statuses := map[string]string{}
	for _, issue := range filled.OpenIssues {
		if issue.Kind == "MISSING_FIELD" {
			statuses[issue.Subject] = issue.Status
		}
	}
	if want := map[string]string{"child": "RESOLVED", "other": "OPEN", "owner": "OPEN"}; !reflect.DeepEqual(statuses, want) {
		t.Fatalf("Reconcile(supplied relationship) statuses = %#v; want %#v", statuses, want)
	}
	if !strings.Contains(strings.Join(decisions, "\n"), "issues_resolved_this_run: 1") {
		t.Fatalf("Reconcile(supplied relationship) decisions = %#v; want one resolution", decisions)
	}
	replay, decisions := reconcile.Reconcile(filled, reconcile.Formatted{People: []reconcile.Proposed{person}}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(replay, filled) || !strings.Contains(strings.Join(decisions, "\n"), "issues_opened_this_run: 0; issues_resolved_this_run: 0") {
		t.Fatalf("Reconcile(relationship replay) changed state or issue totals: %#v", decisions)
	}
}

/*
TestUnappliedPeopleRemainReviewable checks invalid roles are held and ordinary identity holds are not duplicated.

* @brief: TestUnappliedPeopleRemainReviewable -> retains proposals and preserves replay
* @params: t [*testing.T] -> test reporter
*/
func TestUnappliedPeopleRemainReviewable(t *testing.T) {
	people := []reconcile.Proposed{{Role: "unknown", FirstName: "Kai", LastName: "Walker", DOB: strPtr("1980-01-01")}, {Role: "employee", FirstName: "Alex", LastName: "Morgan"}}
	next, _ := reconcile.Reconcile(reconcile.State{CompanyID: "company"}, reconcile.Formatted{People: people}, extractor.Plan{Intent: "SNAPSHOT"})
	if len(next.Employees) != 0 || len(next.OpenIssues) != 2 {
		t.Fatalf("Reconcile(unapplied people) = %#v; want no employees and two retained claims", next)
	}
	for _, issue := range next.OpenIssues {
		var person reconcile.Proposed
		if len(issue.Claims) != 1 || json.Unmarshal([]byte(issue.Claims[0]), &person) != nil || person.FirstName == "" {
			t.Fatalf("Reconcile(unapplied people) lost proposal: %#v", issue)
		}
	}
	replay, _ := reconcile.Reconcile(next, reconcile.Formatted{People: people}, extractor.Plan{Intent: "SNAPSHOT"})
	if !reflect.DeepEqual(replay, next) {
		t.Fatal("Reconcile(unapplied people replay) changed state")
	}
}

/*
TestSummaryCountsHeldIdentityOnceAcrossBenefits checks tally counts identities rather than retained benefit rows.

* @brief: TestSummaryCountsHeldIdentityOnceAcrossBenefits -> separates member and issue counts
* @params: t [*testing.T] -> test reporter
*/
func TestSummaryCountsHeldIdentityOnceAcrossBenefits(t *testing.T) {
	state := reconcile.State{CompanyID: "company", Employees: []reconcile.Employee{{ID: "owner", DOB: strPtr("1980-01-01"), SSN: strPtr("900-11-1111")}}, Dependents: []reconcile.Dependent{{ID: "child", EmployeeID: "owner"}}}
	for _, source := range []string{"M-17", "D-17", "V-17"} {
		person := reconcile.Proposed{Role: "employee", SourceID: source, FirstName: "Alex", LastName: "Morgan"}
		state.OpenIssues = append(state.OpenIssues, reconcile.PersonReviewIssue("company", "INSUFFICIENT_IDENTITY", person))
	}
	state.OpenIssues = append(state.OpenIssues, reconcile.OpenIssue{Kind: "SAME_PERSON", Status: "RESOLVED"})
	got := reconcile.Summarize(state)
	want := reconcile.Summary{Employees: 1, Dependents: 1, HeldIdentities: 1, MembersMissingRequiredFields: 1, OpenIssues: 3, ResolvedIssues: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Summarize(three held benefit claims) = %#v; want %#v", got, want)
	}
}
