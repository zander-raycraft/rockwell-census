package tests

import (
	"census/src/extractor"
	"census/src/parse"
	"census/src/reconcile"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

/*
TestNormalizePlanAppliesSupportedLabels checks that varied spellings retain the same meaning through Format.

* @brief: TestNormalizePlanAppliesSupportedLabels -> checks normalized mappings at the consumer
* @params: t [*testing.T] -> test runner
*/
func TestNormalizePlanAppliesSupportedLabels(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{" Name ", "Member", "Medical"}, Rows: [][]string{{"Robin Walker", "worker", "single"}}}
	input := extractor.Plan{
		DocumentType: " Enrollment ", DateFormat: " mdy ",
		Mappings: []extractor.FieldMap{
			{Destination: " EMPLOYEE.FULL_NAME ", Source: "name"},
			{Destination: "ROW.ROLE", Source: "member"},
			{Destination: "enrollment.coverage_tier", Source: "medical", Literal: " med "},
		},
		Labels: []extractor.LabelMap{
			{Field: "row.role", Source: " worker ", Value: "employee"},
			{Field: "enrollment.coverage_tier", Source: "single", Value: "employee"},
		},
	}
	plan := reconcile.NormalizePlan(input, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		t.Fatalf("validatePlan(normalized enrollment) = %v; want nil", err)
	}
	if input.Mappings[0].Destination != " EMPLOYEE.FULL_NAME " || input.Labels[0].Source != " worker " {
		t.Fatalf("normalizePlan modified caller-owned mappings or labels: %+v", input)
	}
	formatted, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	want := reconcile.Formatted{People: []reconcile.Proposed{{Role: "employee", FirstName: "Robin", LastName: "Walker", Enrollments: []reconcile.Enrollment{{EnrollmentType: "MEDICAL", CoverageTier: strPtr("EMPLOYEE")}}}}, Issues: []reconcile.OpenIssue{}}
	if !reflect.DeepEqual(formatted, want) {
		t.Errorf("Format(normalized enrollment) = %#v; want %#v", formatted, want)
	}
}

/*
TestValidatePlanRejectsUnsafeMappings prevents invalid extraction rules from reaching reconciliation.

* @brief: TestValidatePlanRejectsUnsafeMappings -> rejects unsupported or ambiguous model output
* @params: t [*testing.T] -> test runner
*/
func TestValidatePlanRejectsUnsafeMappings(t *testing.T) {
	cases := []struct {
		name    string
		plan    extractor.Plan
		headers []string
	}{
		{name: "missing header", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Missing"}}}},
		{name: "ambiguous header", headers: []string{"Name", " name "}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}}}},
		{name: "unknown destination", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.nickname", Source: "Name"}}}},
		{name: "literal on person", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name", Literal: "MEDICAL"}}}},
		{name: "invalid benefit", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}, {Destination: "enrollment.plan", Source: "Other", Literal: "LIFE"}}}},
		{name: "conflicting destination", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}, {Destination: "employee.full_name", Source: "Other"}}}},
		{name: "no person", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "enrollment.type", Source: "Other"}}}},
		{name: "empty plan", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY"}},
		{name: "unknown dates", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "GUESS", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}}}},
		{name: "incompatible label", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}}, Labels: []extractor.LabelMap{{Field: "enrollment.status", Source: "Y", Value: "MEDICAL"}}}},
		{name: "conflicting labels", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}}, Labels: []extractor.LabelMap{{Field: "status", Source: "Y", Value: "ENROLLED"}, {Field: "enrollment.status", Source: " y ", Value: "WAIVED"}}}},
		{name: "waiver in tier label", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}}, Labels: []extractor.LabelMap{{Field: "tier", Source: "W", Value: "WAIVED"}}}},
		{name: "blank label", headers: []string{"Name", "Other"}, plan: extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}}, Labels: []extractor.LabelMap{{Field: "status", Source: " ", Value: "WAIVED"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := parse.Parsed{Kind: "csv", Headers: tc.headers}
			if err := reconcile.ValidatePlan(reconcile.NormalizePlan(tc.plan, parsed), parsed); err == nil {
				t.Errorf("validatePlan(%s) = nil; want invalid plan error", tc.name)
			}
		})
	}
}

/*
TestNormalizePlanEmailOwnsResolution checks that email replies remain valid and own their mutable data.

* @brief: TestNormalizePlanEmailOwnsResolution -> preserves email handling and ownership
* @params: t [*testing.T] -> test runner
*/
func TestNormalizePlanEmailOwnsResolution(t *testing.T) {
	parsed := parse.Parsed{Kind: "email"}
	input := extractor.Plan{DateFormat: "MIXED", Uncertainties: []string{"review"}, EmailResolution: &extractor.EmailResolution{Names: []string{" Bob Smith ", "Robert Smith"}, SamePerson: true}}
	plan := reconcile.NormalizePlan(input, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		t.Fatalf("validatePlan(email reply) = %v; want nil", err)
	}
	plan.EmailResolution.Names[0] = "changed"
	plan.EmailResolution.Note = "changed"
	plan.Uncertainties[0] = "changed"
	if input.EmailResolution.Names[0] != " Bob Smith " || input.EmailResolution.Note != "" || input.Uncertainties[0] != "review" {
		t.Errorf("normalizePlan(email reply) shares caller data: %+v", input)
	}
	if err := reconcile.ValidatePlan(reconcile.NormalizePlan(extractor.Plan{DateFormat: "MIXED"}, parsed), parsed); err != nil {
		t.Errorf("validatePlan(email without resolution) = %v; want nil", err)
	}
	plan.Mappings = []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}}
	if err := reconcile.ValidatePlan(plan, parsed); err == nil {
		t.Error("validatePlan(email with table mapping) = nil; want invalid plan error")
	}
}

/*
TestValidatePlanPreservesPartialIdentity accepts documents that supply identifiers without names.

* @brief: TestValidatePlanPreservesPartialIdentity -> keeps incomplete identity supplements usable
* @params: t [*testing.T] -> test runner
*/
func TestValidatePlanPreservesPartialIdentity(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Worker", "SSN"}}
	plan := extractor.Plan{DateFormat: "ISO", Mappings: []extractor.FieldMap{
		{Destination: "employee.source_id", Source: "Worker"},
		{Destination: "employee.ssn", Source: "SSN"},
	}}
	if err := reconcile.ValidatePlan(reconcile.NormalizePlan(plan, parsed), parsed); err != nil {
		t.Errorf("validatePlan(identifier-only supplement) = %v; want nil", err)
	}
}

/*
TestValidatePlanRejectsAuditedInterpretationErrors replays the saved I identity omission and J date failure.

* @brief: TestValidatePlanRejectsAuditedInterpretationErrors -> blocks known harmful plans before writes
* @params: t [*testing.T] -> test runner
*/
func TestValidatePlanRejectsAuditedInterpretationErrors(t *testing.T) {
	for _, tc := range []struct{ file, wantError string }{
		{"company_i_dental_members", "populated identity column"},
		{"company_j_identity_supplement", "conflicts with explicit DMY legend"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			body, err := os.ReadFile("../../../examples/synthetic/batch_02/" + tc.file + ".csv")
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parse.Parse(parse.Document{Filename: tc.file + ".csv", Body: body})
			if err != nil {
				t.Fatal(err)
			}
			body, err = os.ReadFile("testdata/" + tc.file + "_plan.json")
			if err != nil {
				t.Fatal(err)
			}
			var plan extractor.Plan
			if err := json.Unmarshal(body, &plan); err != nil {
				t.Fatal(err)
			}
			plan = reconcile.NormalizePlan(plan, parsed)
			if err := reconcile.ValidatePlan(plan, parsed); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("validatePlan(%s audited plan) = %v; want %q", tc.file, err, tc.wantError)
			}
		})
	}
}

/*
TestValidatePlanAcceptsMemberIdentityRouting preserves employee self identity through mixed-member columns.

* @brief: TestValidatePlanAcceptsMemberIdentityRouting -> validates identity routing supported by Format
* @params: t [*testing.T] -> test runner
*/
func TestValidatePlanAcceptsMemberIdentityRouting(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Subscriber", "Member", "Member DOB", "Member SSN", "Relationship"},
		Rows:    [][]string{{"Alex Morgan", "Alex Morgan", "10/02/1980", "906430001", "Employee"}},
		Skipped: []string{"Dates: DD/MM/YYYY", "Snapshot 2026-10-31"}}
	plan := reconcile.NormalizePlan(extractor.Plan{DateFormat: "DMY", Mappings: []extractor.FieldMap{
		{Destination: "employee.full_name", Source: "Subscriber"},
		{Destination: "dependent.full_name", Source: "Member"},
		{Destination: "dependent.dob", Source: "Member DOB"},
		{Destination: "dependent.ssn", Source: "Member SSN"},
		{Destination: "row.role", Source: "Relationship"},
	}}, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		t.Fatal(err)
	}
	formatted, err := reconcile.Format(plan, parsed, "company")
	if err != nil {
		t.Fatal(err)
	}
	if len(formatted.People) != 1 || formatted.People[0].Role != "employee" || formatted.People[0].SSN == nil || *formatted.People[0].SSN != "906-43-0001" || formatted.People[0].DOB == nil || *formatted.People[0].DOB != "1980-02-10" {
		t.Fatalf("Format(mixed member self row) = %+v; want one employee with DOB 1980-02-10 and SSN 906-43-0001", formatted)
	}
}

/*
TestNormalizePlanIntent preserves legacy plans and rejects unsupported intent values.

* @brief: TestNormalizePlanIntent -> supplies safe legacy defaults and explicit supported intents
* @params: t [*testing.T] -> test runner
*/
func TestNormalizePlanIntent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		parsed parse.Parsed
		plan   extractor.Plan
		want   string
	}{
		{"table", parse.Parsed{Kind: "csv"}, extractor.Plan{}, "SNAPSHOT"},
		{"empty email", parse.Parsed{Kind: "email"}, extractor.Plan{}, "NO_CHANGE"},
		{"same person email", parse.Parsed{Kind: "email"}, extractor.Plan{EmailResolution: &extractor.EmailResolution{}}, "CORRECTION"},
		{"request", parse.Parsed{Kind: "csv"}, extractor.Plan{Intent: " change_request "}, "CHANGE_REQUEST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reconcile.NormalizePlan(tc.plan, tc.parsed).Intent; got != tc.want {
				t.Errorf("normalizePlan(%s).Intent = %q; want %q", tc.name, got, tc.want)
			}
		})
	}
	if err := reconcile.ValidatePlan(extractor.Plan{Intent: "GUESS", DateFormat: "MDY"}, parse.Parsed{Kind: "email"}); err == nil {
		t.Error("validatePlan(unknown intent) = nil; want error")
	}
	if _, err := reconcile.ExplicitDateFormat(parse.Parsed{Skipped: []string{"DD/MM/YYYY", "MM/DD/YYYY"}}); err == nil {
		t.Error("explicitDateFormat(conflicting legends) = nil; want error")
	}
}

/*
TestValidatePlanRejectsInvalidCorrectionSSN rejects a malformed authoritative correction before reconciliation.

* @brief: TestValidatePlanRejectsInvalidCorrectionSSN -> prevents invalid corrections from resolving claims
* @params: t [*testing.T] -> test runner
*/
func TestValidatePlanRejectsInvalidCorrectionSSN(t *testing.T) {
	parsed := parse.Parsed{Kind: "email"}
	plan := reconcile.NormalizePlan(extractor.Plan{Intent: "CORRECTION", DateFormat: "ISO", EmailResolution: &extractor.EmailResolution{Names: []string{"Bob Moses", "Robert Moses"}, SamePerson: true, CorrectSSN: true, SSN: "not-an-ssn"}}, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err == nil {
		t.Error("validatePlan(invalid correction SSN) = nil; want rejection")
	}
}

/*
TestValidatePlanRejectsContradictoryLabelMeanings keeps unknown negatives and different categorical fields distinct.

* @brief: TestValidatePlanRejectsContradictoryLabelMeanings -> prevents confident but unsupported meanings
* @params: t [*testing.T] -> test runner
*/
func TestValidatePlanRejectsContradictoryLabelMeanings(t *testing.T) {
	for _, label := range []extractor.LabelMap{
		{Field: "status", Source: "N", Value: "WAIVED"},
		{Field: "status", Source: "new hire - not elected", Value: "PENDING"},
		{Field: "status", Source: "EE+SP", Value: "ENROLLED"},
		{Field: "tier", Source: "Family", Value: "EMPLOYEE"},
		{Field: "relationship", Source: "Employee", Value: "CHILD"},
		{Field: "employment", Source: "Inactive", Value: "ACTIVE"},
	} {
		if err := reconcile.ValidateLabelMeanings(extractor.Plan{Labels: []extractor.LabelMap{label}}); err == nil {
			t.Errorf("validateLabelMeanings(%+v)=nil; want contradiction error", label)
		}
	}
}

/*
TestValidatePlanUsesLateDateEvidence rejects conventions contradicted after the model's sampled rows.

* @brief: TestValidatePlanUsesLateDateEvidence -> checks all mapped dates without inventing invalid-date fixes
* @params: t [*testing.T] -> test runner
*/
func TestValidatePlanUsesLateDateEvidence(t *testing.T) {
	parsed := parse.Parsed{Kind: "csv", Headers: []string{"Name", "DOB"}}
	for i := 0; i < 30; i++ {
		parsed.Rows = append(parsed.Rows, []string{"Robin Walker", "01/02/1980"})
	}
	parsed.Rows[29][1] = "23/02/1980"
	plan := reconcile.NormalizePlan(extractor.Plan{DateFormat: "MDY", Mappings: []extractor.FieldMap{{Destination: "employee.full_name", Source: "Name"}, {Destination: "employee.dob", Source: "DOB"}}}, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err == nil {
		t.Error("validatePlan(late day-first date)=nil; want contradictory convention error")
	}
	parsed.Rows[29][1] = "02/30/1980"
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		t.Errorf("validatePlan(invalid date)=%v; want row validation to retain invalid-date issue", err)
	}
}
