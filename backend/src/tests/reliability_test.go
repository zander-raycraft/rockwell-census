package tests

import (
	"census/src/extractor"
	"census/src/parse"
	"census/src/reconcile"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

/*
TestCompanyBRetainsPayrollAndAppliesConfirmedBenefits checks actual source facts across document boundaries.

* @brief: TestCompanyBRetainsPayrollAndAppliesConfirmedBenefits -> preserves identity and distinct member elections
* @params: t [*testing.T] -> test reporter
*/
func TestCompanyBRetainsPayrollAndAppliesConfirmedBenefits(t *testing.T) {
	payroll := extractor.Plan{DocumentType: "payroll", Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{
		{Destination: "employee.source_id", Source: "Emp No"},
		{Destination: "employee.first_name", Source: "Legal First Name"},
		{Destination: "employee.last_name", Source: "Legal Last Name"},
		{Destination: "employee.dob", Source: "Date of Birth"},
		{Destination: "employee.ssn", Source: "SSN"},
		{Destination: "employee.hire_date", Source: "Hire Date"},
		{Destination: "employee.employment_status", Source: "Employment Status"},
	}}
	enrollment := extractor.Plan{DocumentType: "enrollment", Intent: "SNAPSHOT", DateFormat: "MDY", Mappings: []extractor.FieldMap{
		{Destination: "employee.source_id", Source: "Employee ID"},
		{Destination: "employee.full_name", Source: "Employee Name"},
		{Destination: "dependent.full_name", Source: "Member Name"},
		{Destination: "dependent.dob", Source: "Member DOB"},
		{Destination: "row.role", Source: "Relationship"},
		{Destination: "dependent.relationship", Source: "Relationship"},
		{Destination: "enrollment.type", Source: "Coverage Type"},
		{Destination: "enrollment.plan", Source: "Plan Name"},
		{Destination: "enrollment.coverage_tier", Source: "Coverage Level"},
		{Destination: "enrollment.status", Source: "Enrollment Status"},
		{Destination: "enrollment.effective_start", Source: "Effective Date"},
	}}
	state := applyReliabilityFile(t, reconcile.State{CompanyID: "company-b"}, "company_b_payroll.csv", payroll)
	var robert reconcile.Employee
	for _, employee := range state.Employees {
		if employee.FirstName == "Robert" && employee.LastName == "Thompson" {
			robert = employee
		}
	}
	if robert.ID == "" {
		t.Fatal("payroll omitted Robert Thompson")
	}
	wantRobert := reconcile.Employee{ID: robert.ID, CompanyID: "company-b", FirstName: "Robert", LastName: "Thompson", DOB: strPtr("1979-02-17"), SSN: strPtr("914-20-5561"), EmploymentStatus: boolTestPtr(true), HireDate: strPtr("2016-04-11"), SourceRefs: robert.SourceRefs}
	if !reflect.DeepEqual(robert, wantRobert) {
		t.Fatalf("Company B payroll Robert = %#v; want %#v", robert, wantRobert)
	}
	state = applyReliabilityFile(t, state, "company_b_enrollment.csv", enrollment)
	state, _ = reconcile.Reconcile(state, reconcile.Formatted{}, extractor.Plan{Intent: "CORRECTION", EmailResolution: &extractor.EmailResolution{Names: []string{"Bob Thompson", "Robert Thompson"}, SamePerson: true}})
	// Confirmation must apply retained spouse facts without uploading the source again.
	var linda reconcile.Dependent
	for _, dependent := range state.Dependents {
		if dependent.FirstName == "Linda" && dependent.LastName == "Thompson" {
			linda = dependent
		}
	}
	wantLinda := reconcile.Dependent{ID: linda.ID, EmployeeID: robert.ID, FirstName: "Linda", LastName: "Thompson", DOB: strPtr("1980-09-02"), Relationship: strPtr("SPOUSE")}
	if linda.ID == "" || !reflect.DeepEqual(linda, wantLinda) {
		t.Errorf("Company B Linda = %#v; want %#v", linda, wantLinda)
	}
	for _, member := range []string{robert.ID, linda.ID} {
		got := map[string]reconcile.Enrollment{}
		for _, e := range state.Enrollments {
			id := e.EmployeeID
			if e.DependentID != nil {
				id = *e.DependentID
			}
			if id == member {
				e.ID = ""
				e.EmployeeID = ""
				e.DependentID = nil
				got[e.EnrollmentType] = e
			}
		}
		want := map[string]reconcile.Enrollment{}
		for benefit, plan := range map[string]string{"MEDICAL": "Aetna Open Access PPO 1500", "DENTAL": "Guardian DHMO", "VISION": "VSP Choice"} {
			want[benefit] = reconcile.Enrollment{EnrollmentType: benefit, Plan: strPtr(plan), CoverageTier: strPtr("EMPLOYEE_SPOUSE_ONLY"), Status: strPtr("ENROLLED"), EffectiveStart: strPtr("2026-01-01")}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Company B member %q coverage = %#v; want %#v", member, got, want)
		}
	}
	for _, employee := range state.Employees {
		if employee.ID == robert.ID && !reflect.DeepEqual(employee.DOB, robert.DOB) {
			t.Errorf("Company B enrollment changed Robert DOB to %v; want %v", employee.DOB, robert.DOB)
		}
	}
	// Samuel's US-format enrollment DOB disagrees with payroll; preserve both claims.
	var samuel reconcile.Employee
	for _, employee := range state.Employees {
		if employee.FirstName == "Samuel" && employee.LastName == "Okonkwo" {
			samuel = employee
		}
	}
	if reconcile.StringValue(samuel.DOB) != "1985-04-03" {
		t.Errorf("Company B Samuel DOB = %q; want preserved payroll 1985-04-03", reconcile.StringValue(samuel.DOB))
	}
	conflictVisible := false
	for _, issue := range state.OpenIssues {
		claims := issue.Detail + strings.Join(issue.Claims, " ")
		if issue.Status == "OPEN" && strings.Contains(claims, "Samuel") && strings.Contains(claims, "1985-03-04") {
			conflictVisible = true
		}
	}
	if !conflictVisible {
		t.Error("Company B discarded Samuel's conflicting enrollment DOB 1985-03-04")
	}
	replay := applyReliabilityFile(t, state, "company_b_enrollment.csv", enrollment)
	if !reflect.DeepEqual(replay, state) {
		t.Errorf("Company B enrollment replay changed state: got %#v; want %#v", replay, state)
	}
}

/*
TestCompanyAHouseholdDoesNotInventCoverage preserves household members whose election cells are blank.

* @brief: TestCompanyAHouseholdDoesNotInventCoverage -> distinguishes family tier from individual dependent elections
* @params: t [*testing.T] -> test reporter
*/
func TestCompanyAHouseholdDoesNotInventCoverage(t *testing.T) {
	plan := extractor.Plan{DocumentType: "census", Intent: "SNAPSHOT", DateFormat: "MDY", Mappings: []extractor.FieldMap{
		{Destination: "employee.full_name", Source: "Name"},
		{Destination: "dependent.full_name", Source: "Name"},
		{Destination: "dependent.dob", Source: "DOB"},
		{Destination: "dependent.ssn", Source: "SSN"},
		{Destination: "row.role", Source: "Relationship"},
		{Destination: "dependent.relationship", Source: "Relationship"},
		{Destination: "employee.hire_date", Source: "Date of Hire"},
		{Destination: "enrollment.coverage_tier", Source: "Medical", Literal: "MEDICAL"},
		{Destination: "enrollment.coverage_tier", Source: "Dental", Literal: "DENTAL"},
		{Destination: "enrollment.coverage_tier", Source: "Vision", Literal: "VISION"},
	}}
	state := applyReliabilityFile(t, reconcile.State{CompanyID: "company-a"}, "company_a_census.csv", plan)
	wantNames := map[string]bool{"Sharon Kowalski": false, "Emma Kowalski": false, "Jake Kowalski": false, "Ngozi Okafor": false, "Mateo Lopez": false}
	for _, dependent := range state.Dependents {
		name := dependent.FirstName + " " + dependent.LastName
		if _, ok := wantNames[name]; ok {
			wantNames[name] = true
		}
	}
	for name, found := range wantNames {
		if !found {
			t.Errorf("Company A omitted household member %s", name)
		}
	}
	for _, enrollment := range state.Enrollments {
		if enrollment.DependentID != nil {
			t.Errorf("Company A invented dependent coverage from blank benefit cells: %#v", enrollment)
		}
	}
	var tamara reconcile.Employee
	for _, employee := range state.Employees {
		if employee.FirstName == "Tamara" {
			tamara = employee
		}
	}
	visionWaived := false
	for _, enrollment := range state.Enrollments {
		if enrollment.EmployeeID == tamara.ID && enrollment.EnrollmentType == "VISION" && reconcile.StringValue(enrollment.Status) == "WAIVED" {
			visionWaived = true
		}
	}
	if !visionWaived {
		t.Error("Company A lost Tamara Reed's explicit vision waiver")
	}
	replay := applyReliabilityFile(t, state, "company_a_census.csv", plan)
	if !reflect.DeepEqual(replay, state) {
		t.Errorf("Company A replay changed state: got %#v; want %#v", replay, state)
	}
}

/*
TestLaterDocumentResolvesOnlySuppliedMissingFact checks complete extraction and reconciliation of later evidence.

* @brief: TestLaterDocumentResolvesOnlySuppliedMissingFact -> preserves issue identity and unrelated unknown facts
* @params: t [*testing.T] -> test reporter
*/
func TestLaterDocumentResolvesOnlySuppliedMissingFact(t *testing.T) {
	plan := extractor.Plan{DocumentType: "payroll", Intent: "SNAPSHOT", DateFormat: "ISO", Mappings: []extractor.FieldMap{
		{Destination: "employee.full_name", Source: "Name"},
		{Destination: "employee.ssn", Source: "SSN"},
		{Destination: "employee.dob", Source: "DOB"},
	}}
	initial := applyReliabilityDocument(t, reconcile.State{CompanyID: "later-evidence"}, parse.Document{Filename: "initial.csv", Body: []byte("Name,SSN,DOB\nMaya Morgan,999-00-0042,\n")}, plan)
	var missing reconcile.OpenIssue
	for _, issue := range initial.OpenIssues {
		if issue.Field == "dob" {
			missing = issue
		}
	}
	if missing.ID == "" || missing.Status != "OPEN" {
		t.Fatalf("initial missing DOB issue = %#v; want stable OPEN issue", missing)
	}
	final := applyReliabilityDocument(t, initial, parse.Document{Filename: "supplement.csv", Body: []byte("Name,SSN,DOB\nMaya Morgan,999-00-0042,1980-01-02\n")}, plan)
	if len(final.Employees) != 1 || reconcile.StringValue(final.Employees[0].DOB) != "1980-01-02" || final.Employees[0].HireDate != nil || len(final.Enrollments) != 0 {
		t.Fatalf("later document state = %#v; want one employee with supplied DOB, unknown hire date, no coverage", final)
	}
	found := false
	for _, issue := range final.OpenIssues {
		if issue.ID == missing.ID {
			found = true
			if issue.Status != "RESOLVED" {
				t.Errorf("later DOB issue = %#v; want original issue resolved", issue)
			}
		}
	}
	if !found {
		t.Errorf("later evidence removed original issue %q instead of resolving it", missing.ID)
	}
	replay := applyReliabilityDocument(t, final, parse.Document{Filename: "supplement.csv", Body: []byte("Name,SSN,DOB\nMaya Morgan,999-00-0042,1980-01-02\n")}, plan)
	if !reflect.DeepEqual(replay, final) {
		t.Errorf("later evidence replay = %#v; want %#v", replay, final)
	}
}

/*
applyReliabilityFile reads an original fixture and applies fixed source interpretation without any model call.

* @brief: applyReliabilityFile -> evaluates real source bytes through parser, formatter, and reconciler
* @params: t [*testing.T] -> test reporter
* @params: state [State] -> state before the document
* @params: filename [string] -> fixture filename relative to examples
* @params: plan [Plan] -> fixed extraction interpretation of source columns
* @returns: next [State] -> state after applying the source document
*/
func applyReliabilityFile(t *testing.T, state reconcile.State, filename string, plan extractor.Plan) reconcile.State {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", filename))
	if err != nil {
		t.Fatal(err)
	}
	return applyReliabilityDocument(t, state, parse.Document{Filename: filename, Body: body}, plan)
}

/*
applyReliabilityDocument applies all non-persistence stages to test semantic document effects.

* @brief: applyReliabilityDocument -> executes fixed-plan extraction without network access
* @params: t [*testing.T] -> test reporter
* @params: state [State] -> state before the document
* @params: document [Document] -> source content
* @params: plan [Plan] -> fixed extraction interpretation
* @returns: next [State] -> reconciled state
*/
func applyReliabilityDocument(t *testing.T, state reconcile.State, document parse.Document, plan extractor.Plan) reconcile.State {
	t.Helper()
	parsed, err := parse.Parse(document)
	if err != nil {
		t.Fatal(err)
	}
	plan = reconcile.NormalizePlan(plan, parsed)
	if err := reconcile.ValidatePlan(plan, parsed); err != nil {
		t.Fatalf("validatePlan(%s): %v", document.Filename, err)
	}
	formatted, err := reconcile.Format(plan, parsed, state.CompanyID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := reconcile.Reconcile(state, formatted, plan)
	return next
}

/*
boolTestPtr provides a literal expected boolean in complete employee expectations.

* @brief: boolTestPtr -> creates expected optional boolean
* @params: value [bool] -> expected boolean value
* @returns: pointer [*bool] -> pointer to expected value
*/
func boolTestPtr(value bool) *bool { return &value }
