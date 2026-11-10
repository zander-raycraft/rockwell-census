package reconcile

import (
	"encoding/json"
	"sort"
	"strings"
)

/*
Formatted is the per-row census proposed by a plan before identity matching.

* @brief: Formatted -> people and row issues produced without looking at the database
* @returns: value [Formatted] -> proposed people and issues from one document
*/
type Formatted struct {
	People       []Proposed  `json:"people"`
	Issues       []OpenIssue `json:"issues"`
	DocumentText string      `json:"document_text,omitempty"`
}

/*
Proposed is one person and that person's enrollments from a single data row.

* @brief: Proposed -> one employee or dependent row before matching
* @returns: value [Proposed] -> name, identity, role, and enrollments from one row
*/
type Proposed struct {
	Role             string       `json:"role"`
	SourceID         string       `json:"source_id"`
	SourceNamespace  string       `json:"source_namespace,omitempty"`
	Subscriber       string       `json:"subscriber"`
	FirstName        string       `json:"first_name"`
	LastName         string       `json:"last_name"`
	DOB              *string      `json:"dob,omitempty"`
	SSN              *string      `json:"ssn,omitempty"`
	EmploymentStatus *bool        `json:"employment_status,omitempty"`
	HireDate         *string      `json:"hire_date,omitempty"`
	Relationship     *string      `json:"relationship,omitempty"`
	Enrollments      []Enrollment `json:"enrollments"`
	Notes            []string     `json:"notes,omitempty"`
}

type rowBuild struct {
	empFirst        string
	empLast         string
	depFirst        string
	depLast         string
	role            string
	roleConflict    bool
	relSeen         bool
	relConflict     bool
	relRaw          string
	relationship    *string
	sourceID        string
	subscriberID    string
	employeeDOB     *string
	dependentDOB    *string
	employeeSSN     *string
	dependentSSN    *string
	employment      *bool
	employmentRaw   string
	employmentNotes []string
	hire            *string
	notes           []string
	benefits        map[string]*benefitAcc
	loose           benefitAcc
	sharedStart     *string
	sharedEnd       *string
	rowType         string
	pending         []pendingIssue
}

type benefitAcc struct {
	typ    string
	plan   *string
	tier   *string
	status *string
	start  *string
	end    *string
}

type pendingIssue struct {
	kind   string
	detail string
	field  string
	claims []string
}

type formattedRow struct {
	Proposed *Proposed
	Issues   []OpenIssue
}

/*
Format applies an extraction plan to every parsed data row.

* @brief: Format -> maps each data row into people and row issues
* @params: plan [Plan] -> mappings, labels, and date format
* @params: parsed [Parsed] -> header and every data row
* @params: companyID [string] -> company id copied onto row issues
* @returns: formatted [Formatted] -> proposed people and issues
* @returns: err [error] -> non-nil when the plan cannot be applied
*/
func Format(plan Plan, parsed Parsed, companyID string) (Formatted, error) {
	out := Formatted{People: []Proposed{}, Issues: []OpenIssue{}}
	if parsed.Kind != "csv" {
		out.DocumentText = parsed.Preview
		return out, nil
	}
	if plan.Intent == "NO_CHANGE" {
		return out, nil
	}
	dateFormat := strings.ToUpper(strings.TrimSpace(plan.DateFormat))
	switch dateFormat {
	case "MDY", "DMY", "ISO", "MIXED":
	case "":
		dateFormat = "MIXED"
	default:
		out.Issues = append(out.Issues, openRowIssue(companyID, "UNKNOWN_VALUE", "date format "+plan.DateFormat+" is unknown; using MIXED"))
		dateFormat = "MIXED"
	}
	seenDest := map[string]bool{}
	seenMissing := map[string]bool{}
	for _, mapping := range plan.Mappings {
		dest := strings.TrimSpace(mapping.Destination)
		if dest == "" {
			continue
		}
		if !knownDestination(dest) && !seenDest[dest] {
			seenDest[dest] = true
			out.Issues = append(out.Issues, openRowIssue(companyID, "UNKNOWN_DESTINATION", "unknown destination "+dest))
		}
		source := strings.TrimSpace(mapping.Source)
		if source == "" || seenMissing[source] {
			continue
		}
		if _, ok := columnIndex(parsed.Headers, source); !ok {
			seenMissing[source] = true
			out.Issues = append(out.Issues, openRowIssue(companyID, "UNKNOWN_COLUMN", "column not in header: "+source))
		}
	}
	lastEmployee := ""
	for i, row := range parsed.Rows {
		person := formatRow(plan, parsed.Headers, row, i+1, companyID, dateFormat)
		if len(person.Issues) > 0 {
			out.Issues = append(out.Issues, person.Issues...)
		}
		if person.Proposed == nil {
			continue
		}
		proposed := *person.Proposed
		proposed.SourceNamespace = SourceReference(proposed.SourceID).Namespace
		if proposed.Role == "dependent" && proposed.Subscriber == "" && proposed.SourceID == "" && lastEmployee != "" {
			proposed.Subscriber = lastEmployee
			proposed.Notes = append(proposed.Notes, "subscriber taken from the preceding employee row")
		}
		if proposed.Role == "employee" {
			if name := strings.TrimSpace(proposed.FirstName + " " + proposed.LastName); name != "" {
				lastEmployee = name
			}
		}
		out.People = append(out.People, proposed)
	}
	return out, nil
}

/*
formatRow maps one data row into a proposed person and row issues.

* @brief: formatRow -> applies mappings to one data row
* @params: plan [Plan] -> mappings and labels
* @params: headers [[]string] -> column names
* @params: row [[]string] -> one data row
* @params: rowNumber [int] -> one-based data row number
* @params: companyID [string] -> company id for issues
* @params: dateFormat [string] -> MDY, DMY, ISO, or MIXED
* @returns: row [formattedRow] -> one person, or issues only when the row is empty
*/
func formatRow(plan Plan, headers, row []string, rowNumber int, companyID, dateFormat string) formattedRow {
	built := &rowBuild{benefits: map[string]*benefitAcc{}}
	for _, mapping := range plan.Mappings {
		if strings.TrimSpace(mapping.Destination) == "employee.full_name" || strings.TrimSpace(mapping.Destination) == "dependent.full_name" {
			applyNameMapping(built, headers, row, mapping)
		}
	}
	for _, mapping := range plan.Mappings {
		dest := strings.TrimSpace(mapping.Destination)
		if dest == "employee.full_name" || dest == "dependent.full_name" {
			continue
		}
		applyMapping(built, plan.Labels, headers, row, mapping, dateFormat)
	}
	proposed := built.proposed(dateFormat)
	prefix := rowPrefix(rowNumber, proposed)
	var issues []OpenIssue
	for _, item := range built.pending {
		issue := openRowIssue(companyID, item.kind, prefix+": "+item.detail)
		issue.Subject = prefix
		issue.Claims = []string{item.detail}
		issue.Field = item.field
		if len(item.claims) > 0 {
			issue.Claims = append([]string(nil), item.claims...)
		}
		issues = append(issues, issue)
	}
	unmapped := unmappedSourceIssues(plan, headers, row, companyID, prefix)
	var heldPerson []byte
	for i := range unmapped {
		if unmapped[i].Field == "dependent.relationship" {
			if heldPerson == nil {
				heldPerson, _ = json.Marshal(proposed)
			}
			unmapped[i].Claims = append(unmapped[i].Claims, string(heldPerson))
			proposed = nil
		}
	}
	issues = append(issues, unmapped...)
	if proposed != nil && (built.roleConflict || (built.role == "" && built.relSeen)) {
		issues = append(issues, PersonReviewIssue(companyID, "AMBIGUOUS_ROW_ROLE", *proposed))
		proposed = nil
	}
	if proposed == nil {
		return formattedRow{Issues: issues}
	}
	return formattedRow{Proposed: proposed, Issues: issues}
}

/*
applyNameMapping splits a full-name column into first and last name.

* @brief: applyNameMapping -> stores a full name on the employee or dependent side
* @params: built [*rowBuild] -> row being formatted
* @params: headers [[]string] -> column names
* @params: row [[]string] -> data cells
* @params: mapping [FieldMap] -> full-name mapping
*/
func applyNameMapping(built *rowBuild, headers, row []string, mapping FieldMap) {
	cell, ok := mappedCell(headers, row, mapping.Source)
	if !ok || cell == "" {
		return
	}
	first, last := SplitName(cell)
	switch strings.TrimSpace(mapping.Destination) {
	case "dependent.full_name":
		if built.depFirst == "" {
			built.depFirst = first
		}
		if built.depLast == "" {
			built.depLast = last
		}
	default:
		if built.empFirst == "" {
			built.empFirst = first
		}
		if built.empLast == "" {
			built.empLast = last
		}
	}
}

/*
applyMapping stores one non-name mapping on the row.

* @brief: applyMapping -> writes one mapped cell onto the row
* @params: built [*rowBuild] -> row being formatted
* @params: labels [[]LabelMap] -> canonical label table
* @params: headers [[]string] -> column names
* @params: row [[]string] -> data cells
* @params: mapping [FieldMap] -> column mapping
* @params: dateFormat [string] -> date convention for this document
*/
func applyMapping(built *rowBuild, labels []LabelMap, headers, row []string, mapping FieldMap, dateFormat string) {
	dest := strings.TrimSpace(mapping.Destination)
	if !knownDestination(dest) {
		return
	}
	cell, ok := mappedCell(headers, row, mapping.Source)
	if mapping.Source != "" && !ok {
		return
	}
	if mapping.Source != "" && cell == "" && dest != "enrollment.type" && dest != "dependent.relationship" && dest != "row.role" {
		return
	}
	switch dest {
	case "employee.first_name":
		built.empFirst = cell
	case "employee.last_name":
		built.empLast = cell
	case "dependent.first_name":
		built.depFirst = cell
	case "dependent.last_name":
		built.depLast = cell
	case "employee.source_id":
		built.sourceID = cell
	case "dependent.subscriber_id":
		built.subscriberID = cell
	case "row.role", "dependent.relationship":
		built.applyRelationship(cell, labels)
	case "employee.dob":
		built.employeeDOB = built.parseDateCell(cell, dateFormat, "dob")
	case "dependent.dob":
		built.dependentDOB = built.parseDateCell(cell, dateFormat, "dob")
	case "employee.hire_date":
		built.hire = built.parseDateCell(cell, dateFormat, "hire date")
	case "employee.ssn":
		built.employeeSSN = built.parseSSN(cell)
	case "dependent.ssn":
		built.dependentSSN = built.parseSSN(cell)
	case "employee.employment_notes":
		built.employmentNotes = append(built.employmentNotes, cell)
	case "employee.employment_status":
		status, okStatus := parseEmployment(cell, labels)
		if !okStatus {
			built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_VALUE", detail: "unknown employment status " + quote(cell)})
			return
		}
		built.employment = status
		built.employmentRaw = cell
	case "enrollment.type":
		value := labeled(labels, "type", cell)
		benefit, okBenefit := canonBenefit(value)
		if !okBenefit {
			built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_VALUE", detail: "unknown enrollment type " + quote(cell)})
			return
		}
		built.rowType = benefit
	case "enrollment.coverage_tier":
		if cell == "" {
			return
		}
		tier, status, okTier := interpretTierCell(cell, labels)
		if !okTier {
			built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_TIER", detail: "unknown coverage tier " + quote(cell)})
			return
		}
		acc := built.ensure(mapping.Literal)
		if tier != "" {
			assignString(&acc.tier, tier)
		}
		if status != "" {
			assignString(&acc.status, status)
		}
	case "enrollment.status":
		value := labeled(labels, "status", cell)
		status, okStatus := canonStatusValue(value)
		parts := strings.Fields(cell)
		if len(parts) == 2 && strings.EqualFold(parts[0], "terminated") {
			if okStatus && status != "TERMINATED" {
				built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_STATUS", detail: "status label contradicts " + quote(cell)})
				return
			}
			status, okStatus = "TERMINATED", true
			built.ensure(mapping.Literal).end = built.parseDateCell(parts[1], dateFormat, "termination date")
		}
		if !okStatus {
			built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_STATUS", detail: "unknown enrollment status " + quote(cell)})
			return
		}
		assignString(&built.ensure(mapping.Literal).status, status)
	case "enrollment.plan":
		assignString(&built.ensure(mapping.Literal).plan, cell)
	case "enrollment.effective_start":
		parsed := built.parseDateCell(cell, dateFormat, "effective start")
		if benefitLiteral(mapping.Literal) == "" {
			built.sharedStart = parsed
			return
		}
		built.ensure(mapping.Literal).start = parsed
	case "enrollment.effective_end":
		parsed := built.parseDateCell(cell, dateFormat, "effective end")
		if benefitLiteral(mapping.Literal) == "" {
			built.sharedEnd = parsed
			return
		}
		built.ensure(mapping.Literal).end = parsed
	}
}

/*
applyRelationship keeps role and relationship from the same canonical cell together.

* @brief: applyRelationship -> retains relationships without requiring duplicate mappings
* @params: cell [string] -> original role or relationship value
* @params: labels [[]LabelMap] -> explicit source label meanings
*/
func (built *rowBuild) applyRelationship(cell string, labels []LabelMap) {
	built.relSeen = true
	if cell == "" {
		return
	}
	built.relRaw = cell
	relationship, role, known := classifyRelationship(labeled(labels, "relationship", cell))
	if !known {
		built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_RELATIONSHIP", detail: "unknown relationship " + quote(cell)})
		return
	}
	if built.role != "" && built.role != role {
		built.roleConflict = true
		built.pending = append(built.pending, pendingIssue{kind: "CONFLICT", detail: "conflicting row role " + quote(cell) + " versus " + built.role})
	} else {
		built.role = role
	}
	if relationship == "" || built.relConflict {
		return
	}
	if built.relationship != nil && *built.relationship != relationship {
		built.pending = append(built.pending, pendingIssue{kind: "CONFLICT", detail: "conflicting relationship " + quote(cell) + " versus " + *built.relationship})
		built.relationship = nil
		built.relConflict = true
		return
	}
	built.relationship = strPtr(relationship)
}

/*
unmappedSourceIssues retains recognized source evidence omitted or sent to unrelated fields.

* @brief: unmappedSourceIssues -> exposes unmapped relationship and status cells for review
* @params: plan [Plan] -> applied extraction mappings
* @params: headers [[]string] -> source column names
* @params: row [[]string] -> original data cells
* @params: companyID [string] -> company receiving review issues
* @params: subject [string] -> row identity used for stable issue deduplication
* @returns: issues [[]OpenIssue] -> raw column/value claims; ancillary columns are ignored
*/
func unmappedSourceIssues(plan Plan, headers, row []string, companyID, subject string) []OpenIssue {
	var issues []OpenIssue
	for i, header := range headers {
		if i >= len(row) || strings.TrimSpace(row[i]) == "" {
			continue
		}
		field := ""
		switch normKey(header) {
		case "relationship", "rel", "relcode", "relationshipcode":
			field = "dependent.relationship"
		case "employment", "employmentstatus", "workerstatus":
			field = "employee.employment_status"
		case "status":
			field = "status"
		case "enrollmentstatus", "electionstatus", "medicalstatus", "dentalstatus", "visionstatus":
			field = "enrollment.status"
		}
		if field == "" {
			continue
		}
		mapped := false
		for _, mapping := range plan.Mappings {
			if mapping.Source != header {
				continue
			}
			mapped = mapped || mapping.Destination == field || (field == "dependent.relationship" && mapping.Destination == "row.role")
			if field == "status" && (mapping.Destination == "employee.employment_status" || mapping.Destination == "enrollment.status") {
				mapped = true
			}
			// Wide benefit cells may explicitly encode status through a tier mapping.
			if (field == "enrollment.status" || field == "status") && mapping.Destination == "enrollment.coverage_tier" {
				_, status, known := interpretTierCell(row[i], plan.Labels)
				mapped = mapped || (known && status != "")
			}
		}
		if !mapped {
			raw, _ := json.Marshal(map[string]string{"column": header, "value": row[i]})
			issues = append(issues, OpenIssue{CompanyID: companyID, Kind: "UNMAPPED_SOURCE_FIELD", Subject: subject, Field: field, Claims: []string{string(raw)}, Detail: subject + ": unapplied " + header + " " + quote(row[i]), Status: "OPEN"})
		}
	}
	return issues
}

/*
parseDateCell parses a date cell and records INVALID_DATE when the calendar date is impossible.

* @brief: parseDateCell -> parses one date cell
* @params: cell [string] -> raw date text
* @params: dateFormat [string] -> MDY, DMY, ISO, or MIXED
* @params: field [string] -> field name used in an issue
* @returns: value [*string] -> ISO date, or nil when blank or invalid
*/
func (built *rowBuild) parseDateCell(cell, dateFormat, field string) *string {
	value, present, valid := parseDate(cell, dateFormat)
	if !present {
		return nil
	}
	if !valid {
		built.pending = append(built.pending, pendingIssue{kind: "INVALID_DATE", detail: "invalid date " + field + " " + quote(cell)})
		return nil
	}
	return strPtr(value)
}

/*
parseSSN normalizes nine digits and keeps any other non-empty text.

* @brief: parseSSN -> normalizes a Social Security number cell
* @params: cell [string] -> raw SSN text
* @returns: value [*string] -> formatted SSN, trimmed text, or nil when blank
*/
func (built *rowBuild) parseSSN(cell string) *string {
	formatted, nine := normalizeSSN(cell)
	if formatted == "" {
		return nil
	}
	if !nine {
		built.pending = append(built.pending, pendingIssue{kind: "INVALID_SSN", detail: "SSN " + quote(cell) + " is not 9 digits"})
	}
	return strPtr(formatted)
}

/*
ensure returns the benefit bucket for a literal, or the loose bucket when the literal is empty.

* @brief: ensure -> returns the enrollment bucket for one benefit literal
* @params: literal [string] -> MEDICAL, DENTAL, VISION, or empty
* @returns: acc [*benefitAcc] -> enrollment fields for that benefit
*/
func (built *rowBuild) ensure(literal string) *benefitAcc {
	benefit := benefitLiteral(literal)
	if benefit == "" {
		return &built.loose
	}
	if built.benefits[benefit] == nil {
		built.benefits[benefit] = &benefitAcc{typ: benefit}
	}
	return built.benefits[benefit]
}

/*
proposed finishes role, name, and enrollment decisions for the row.

* @brief: proposed -> builds the person represented by the row
* @params: dateFormat [string] -> document date convention for employment notes
* @returns: person [*Proposed] -> nil when the row has no identity and no enrollment
*/
func (built *rowBuild) proposed(dateFormat string) *Proposed {
	role := built.role
	empName := strings.TrimSpace(built.empFirst + " " + built.empLast)
	depName := strings.TrimSpace(built.depFirst + " " + built.depLast)
	if role == "" && !built.relSeen {
		role = "employee"
		if empName != "" && depName != "" && !samePersonText(empName, depName) {
			role = "dependent"
		}
	}
	if role == "employee" {
		for _, note := range built.employmentNotes {
			built.notes = append(built.notes, note)
			status, relevant := parseEmploymentNote(note, dateFormat)
			if !relevant {
				continue
			}
			if status == nil {
				claims := []string{note}
				if built.employmentRaw != "" {
					claims = append([]string{built.employmentRaw}, claims...)
				}
				built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_VALUE", detail: "employment note needs review: " + quote(note), field: "employment_status", claims: claims})
				continue
			}
			if built.employment != nil && *built.employment != *status {
				built.pending = append(built.pending, pendingIssue{kind: "CONFLICT_EMPLOYMENT_STATUS", detail: "employment status " + quote(built.employmentRaw) + " contradicts note: " + quote(note), field: "employment_status", claims: []string{built.employmentRaw, note}})
				built.employment = nil
				break
			}
			built.employment = status
		}
	}
	person := Proposed{Role: role, SourceID: built.sourceID, EmploymentStatus: cloneBool(built.employment), HireDate: CloneString(built.hire), Notes: append([]string(nil), built.notes...), Enrollments: []Enrollment{}}
	if role != "employee" {
		if built.subscriberID != "" {
			person.SourceID = built.subscriberID
		}
		if depName != "" {
			person.FirstName, person.LastName = built.depFirst, built.depLast
		} else {
			person.FirstName, person.LastName = built.empFirst, built.empLast
		}
		if empName != "" && depName != "" && !samePersonText(empName, depName) {
			person.Subscriber = empName
		}
		person.Relationship = CloneString(built.relationship)
		person.DOB = firstString(built.dependentDOB, built.employeeDOB)
		person.SSN = firstString(built.dependentSSN, built.employeeSSN)
	} else {
		if empName != "" {
			person.FirstName, person.LastName = built.empFirst, built.empLast
		} else {
			person.FirstName, person.LastName = built.depFirst, built.depLast
		}
		person.DOB = firstString(built.employeeDOB, built.dependentDOB)
		person.SSN = firstString(built.employeeSSN, built.dependentSSN)
	}
	built.finishBenefits()
	for _, acc := range orderedBenefits(built.benefits) {
		enrollment, ok := acc.enrollment()
		if ok {
			person.Enrollments = append(person.Enrollments, enrollment)
		}
	}
	if strings.TrimSpace(person.FirstName) == "" && strings.TrimSpace(person.LastName) == "" &&
		person.SSN == nil && person.SourceID == "" && len(person.Enrollments) == 0 && person.DOB == nil {
		return nil
	}
	return &person
}

/*
finishBenefits attaches loose fields and shared dates to the row's benefit buckets.

* @brief: finishBenefits -> copies shared dates onto each benefit enrollment
*/
func (built *rowBuild) finishBenefits() {
	if built.rowType != "" {
		built.ensure(built.rowType).fillFrom(built.loose)
	} else if len(built.benefits) == 1 && built.loose.hasData() {
		for _, acc := range built.benefits {
			acc.fillFrom(built.loose)
		}
	} else if len(built.benefits) == 0 && built.loose.hasData() {
		built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_VALUE", detail: "enrollment fields have no benefit type"})
	} else if len(built.benefits) > 1 && built.loose.hasData() {
		built.pending = append(built.pending, pendingIssue{kind: "UNKNOWN_VALUE", detail: "unscoped enrollment field was not applied"})
	}
	for _, acc := range built.benefits {
		if built.sharedStart != nil && acc.start == nil {
			acc.start = CloneString(built.sharedStart)
		}
		if built.sharedEnd != nil && acc.end == nil {
			acc.end = CloneString(built.sharedEnd)
		}
	}
}

/*
fillFrom copies empty enrollment fields from another bucket.

* @brief: fillFrom -> fills empty benefit fields
* @params: other [benefitAcc] -> fields to copy when this bucket is empty
*/
func (acc *benefitAcc) fillFrom(other benefitAcc) {
	if acc.plan == nil {
		acc.plan = CloneString(other.plan)
	}
	if acc.tier == nil {
		acc.tier = CloneString(other.tier)
	}
	if acc.status == nil {
		acc.status = CloneString(other.status)
	}
	if acc.start == nil {
		acc.start = CloneString(other.start)
	}
	if acc.end == nil {
		acc.end = CloneString(other.end)
	}
}

/*
enrollment converts a benefit bucket into an enrollment.

* @brief: enrollment -> builds an enrollment from a benefit bucket
* @returns: enrollment [Enrollment] -> one benefit record
* @returns: ok [bool] -> false when the bucket has no election fields
*/
func (acc *benefitAcc) enrollment() (Enrollment, bool) {
	if acc == nil || acc.typ == "" || !acc.hasData() {
		return Enrollment{}, false
	}
	return Enrollment{
		EnrollmentType: acc.typ,
		Plan:           CloneString(acc.plan),
		CoverageTier:   CloneString(acc.tier),
		Status:         CloneString(acc.status),
		EffectiveStart: CloneString(acc.start),
		EffectiveEnd:   CloneString(acc.end),
	}, true
}

/*
orderedBenefits lists benefit buckets in medical, dental, vision order.

* @brief: orderedBenefits -> returns benefit buckets in a stable order
* @params: benefits [map[string]*benefitAcc] -> buckets keyed by benefit type
* @returns: list [[]*benefitAcc] -> buckets in display order
*/
func orderedBenefits(benefits map[string]*benefitAcc) []*benefitAcc {
	var out []*benefitAcc
	seen := map[string]bool{}
	for _, typ := range []string{"MEDICAL", "DENTAL", "VISION"} {
		if acc := benefits[typ]; acc != nil {
			out = append(out, acc)
			seen[typ] = true
		}
	}
	var rest []string
	for typ := range benefits {
		if !seen[typ] {
			rest = append(rest, typ)
		}
	}
	sort.Strings(rest)
	for _, typ := range rest {
		out = append(out, benefits[typ])
	}
	return out
}

/*
hasData reports whether a benefit bucket has any election field.

* @brief: hasData -> reports whether a benefit bucket should become an enrollment
* @returns: ok [bool] -> true when plan, tier, status, or a date is set
*/
func (acc *benefitAcc) hasData() bool {
	return acc != nil && (acc.plan != nil || acc.tier != nil || acc.status != nil || acc.start != nil || acc.end != nil)
}
