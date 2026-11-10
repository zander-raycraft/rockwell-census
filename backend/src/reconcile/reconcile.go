package reconcile

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

/*
Reconcile copies state and applies document intent, formatted people, and email resolution.

* @brief: Reconcile -> matches people and returns the next state plus decision lines
* @params: state [State] -> company census before this document
* @params: formatted [Formatted] -> people and row issues from Format
* @params: plan [Plan] -> document intent and optional email resolution
* @returns: next [State] -> census after this document
* @returns: decisions [[]string] -> lines for the MAP log
*/
func Reconcile(state State, formatted Formatted, plan Plan) (next State, decisions []string) {
	next = CopyState(state)
	decisions = []string{}
	defer func() { recordSummary(state, next, &decisions) }()
	if plan.Intent == "NO_CHANGE" {
		return next, decisions
	}
	if plan.Intent == "CHANGE_REQUEST" || (plan.Intent == "CORRECTION" && plan.EmailResolution == nil) {
		kind := "UNAPPROVED_CHANGE"
		if plan.Intent == "CORRECTION" {
			kind = "UNSUPPORTED_CORRECTION"
		}
		for _, person := range formatted.People {
			AddIssue(&next, &decisions, PersonReviewIssue(next.CompanyID, kind, person))
		}
		if len(formatted.People) == 0 && (formatted.DocumentText != "" || len(formatted.Issues) == 0) {
			claim, _ := json.Marshal(struct {
				DocumentText  string   `json:"document_text"`
				Intent        string   `json:"intent"`
				Uncertainties []string `json:"uncertainties"`
			}{formatted.DocumentText, plan.Intent, plan.Uncertainties})
			source := formatted.DocumentText
			if source == "" {
				source = string(claim)
			}
			issue := OpenIssue{
				CompanyID: next.CompanyID, Kind: kind, Field: "document",
				Subject: fmt.Sprintf("document:%x", sha256.Sum256([]byte(source))),
				Detail:  kind + ": review retained document before applying changes", Status: "OPEN",
			}
			issue.Key = issueKey(issue)
			issue.Claims = []string{string(claim)}
			AddIssue(&next, &decisions, issue)
		}
		for _, issue := range formatted.Issues {
			AddIssue(&next, &decisions, issue)
		}
		return next, decisions
	}
	sources := indexSources(next.Employees)
	var dependents []Proposed
	var accepted []Proposed
	for _, person := range formatted.People {
		if person.SourceNamespace == "" {
			person.SourceNamespace = SourceReference(person.SourceID).Namespace
		}
		if person.Role == "dependent" {
			dependents = append(dependents, person)
			continue
		}
		if person.Role != "employee" {
			AddIssue(&next, &decisions, PersonReviewIssue(next.CompanyID, "AMBIGUOUS_ROW_ROLE", person))
			continue
		}
		if applyEmployee(&next, person, sources, &decisions) {
			accepted = append(accepted, person)
		} else {
			retainUnapplied(&next, person, &decisions)
		}
	}
	for _, person := range dependents {
		if applyDependent(&next, person, sources, &decisions) {
			accepted = append(accepted, person)
		} else {
			retainUnapplied(&next, person, &decisions)
		}
	}
	for _, issue := range formatted.Issues {
		AddIssue(&next, &decisions, issue)
	}
	applyEmailResolution(&next, plan, &decisions)
	resolveHeldMembers(&next, accepted, &decisions)
	applyHeldClaims(&next, sources, &decisions)
	refreshMissingIssues(&next, &decisions)
	refreshCoverageIssues(&next, &decisions)
	return next, decisions
}

/*
applyEmployee matches or adds one employee and attaches that employee's enrollments.

* @brief: applyEmployee -> updates one employee in the next state
* @params: next [*State] -> census being updated
* @params: person [Proposed] -> employee row
* @params: sources [map[string][]string] -> source id to employee ids seen in this document
* @params: decisions [*[]string] -> MAP lines
* @returns: applied [bool] -> true when the identity was matched or inserted
*/
func applyEmployee(next *State, person Proposed, sources map[string][]string, decisions *[]string) bool {
	index, how, ambiguous := matchEmployee(next.Employees, person)
	identityIndex := index
	key := person.SourceNamespace + "|" + strings.TrimSpace(person.SourceID)
	if person.SourceNamespace != "" {
		ids := uniqueStrings(sources[key])
		if len(ids) > 1 {
			ambiguous = true
		} else if len(ids) == 1 {
			linked := employeeIndex(next.Employees, ids[0])
			if linked >= 0 {
				stored := next.Employees[linked]
				conflicting := stored.SSN != nil && person.SSN != nil && !sameSSNText(*stored.SSN, *person.SSN)
				if conflicting || (index >= 0 && index != linked) {
					ambiguous = true
				} else if !ambiguous {
					index, how = linked, "source reference"
				}
			}
		}
	}
	if ambiguous {
		AddIssue(next, decisions, PersonReviewIssue(next.CompanyID, "AMBIGUOUS_MATCH", person))
		return false
	}
	if index < 0 {
		if person.SSN == nil && person.DOB == nil {
			AddIssue(next, decisions, PersonReviewIssue(next.CompanyID, "INSUFFICIENT_IDENTITY", person))
			return false
		}
		for _, employee := range next.Employees {
			// Distinct dates and identifiers support two people even when names agree.
			distinct := person.DOB != nil && employee.DOB != nil && *person.DOB != *employee.DOB &&
				person.SSN != nil && employee.SSN != nil && !sameSSNText(*person.SSN, *employee.SSN)
			sameName := keysOverlap(personKeys(employee.FirstName, employee.LastName), personKeys(person.FirstName, person.LastName))
			sameSurname := normKey(employee.LastName) == normKey(person.LastName) && normKey(person.LastName) != ""
			if !distinct && (sameName || (sameSurname && person.DOB != nil && employee.DOB != nil && *person.DOB == *employee.DOB && person.SSN == nil)) {
				AddIssue(next, decisions, PersonReviewIssue(next.CompanyID, "POSSIBLE_MATCH", person))
				return false
			}
		}
	}
	var employeeID string
	if index >= 0 {
		employeeID = next.Employees[index].ID
		*decisions = append(*decisions, "matched employee "+DisplayName(person.FirstName, person.LastName)+" on "+how)
		fillEmployee(&next.Employees[index], person, next, decisions)
	} else {
		id, ok := freshID(decisions)
		if !ok {
			return false
		}
		employeeID = id
		next.Employees = append(next.Employees, Employee{
			ID:               id,
			CompanyID:        next.CompanyID,
			FirstName:        person.FirstName,
			LastName:         person.LastName,
			DOB:              CloneString(person.DOB),
			SSN:              CloneString(person.SSN),
			EmploymentStatus: cloneBool(person.EmploymentStatus),
			HireDate:         CloneString(person.HireDate),
		})
		*decisions = append(*decisions, "added employee "+DisplayName(person.FirstName, person.LastName))
	}
	if person.SourceNamespace != "" && person.SourceID != "" {
		employee := employeePtr(next, employeeID)
		ref := SourceRef{Namespace: person.SourceNamespace, Value: strings.TrimSpace(person.SourceID)}
		rememberReference(employee, ref)
	}
	if person.SourceID != "" {
		rememberSource(sources, key, employeeID)
		if identityIndex >= 0 && identityIndex == index &&
			(person.DOB == nil || next.Employees[index].DOB == nil || *person.DOB == *next.Employees[index].DOB) {
			rememberSource(sources, sourceNameKey(key, DisplayName(person.FirstName, person.LastName)), employeeID)
		}
	}
	attachEnrollments(next, employeeID, nil, person.Enrollments, DisplayName(person.FirstName, person.LastName), decisions)
	return true
}

/*
applyDependent attaches a dependent to exactly one employee or opens an unmatched issue.

* @brief: applyDependent -> links one dependent to a parent employee
* @params: next [*State] -> census being updated
* @params: person [Proposed] -> dependent row
* @params: sources [map[string][]string] -> source ids from employees in this document
* @params: decisions [*[]string] -> MAP lines
* @returns: applied [bool] -> true when the identity was matched or inserted
*/
func applyDependent(next *State, person Proposed, sources map[string][]string, decisions *[]string) bool {
	parentID, ok := findParent(next.Employees, sources, person)
	if !ok {
		AddIssue(next, decisions, PersonReviewIssue(next.CompanyID, "UNMATCHED_DEPENDENT", person))
		return false
	}
	index, how, ambiguous := matchDependent(next.Dependents, parentID, person)
	if ambiguous {
		issue := PersonReviewIssue(next.CompanyID, "AMBIGUOUS_MATCH", person)
		issue.EmployeeID = strPtr(parentID)
		AddIssue(next, decisions, issue)
		return false
	}
	var dependentID string
	if index >= 0 {
		dependentID = next.Dependents[index].ID
		*decisions = append(*decisions, "matched dependent "+DisplayName(person.FirstName, person.LastName)+" on "+how)
		FillDependent(&next.Dependents[index], person, next, decisions)
	} else {
		id, okID := freshID(decisions)
		if !okID {
			return false
		}
		dependentID = id
		next.Dependents = append(next.Dependents, Dependent{
			ID:           id,
			EmployeeID:   parentID,
			FirstName:    person.FirstName,
			LastName:     person.LastName,
			DOB:          CloneString(person.DOB),
			SSN:          CloneString(person.SSN),
			Relationship: CloneString(person.Relationship),
		})
		parent := EmployeeByID(next.Employees, parentID)
		*decisions = append(*decisions, "added dependent "+DisplayName(person.FirstName, person.LastName)+" under "+parent)
	}
	attachEnrollments(next, parentID, strPtr(dependentID), person.Enrollments, DisplayName(person.FirstName, person.LastName), decisions)
	return true
}

/*
matchEmployee finds one employee by SSN or uniquely compatible name and identity evidence.

* @brief: matchEmployee -> finds a confident employee match
* @params: employees [[]Employee] -> employees in this company
* @params: person [Proposed] -> incoming employee
* @returns: index [int] -> matching index, or -1
* @returns: how [string] -> matching evidence recorded in the MAP log
* @returns: ambiguous [bool] -> true when more than one employee matches
*/
func matchEmployee(employees []Employee, person Proposed) (int, string, bool) {
	if person.SSN != nil && len(digitsOnly(*person.SSN)) == 9 {
		var hits []int
		for i, employee := range employees {
			if employee.SSN != nil && digitsOnly(*employee.SSN) == digitsOnly(*person.SSN) {
				hits = append(hits, i)
			}
		}
		if len(hits) == 1 {
			return hits[0], "SSN", false
		}
		if len(hits) > 1 {
			return -1, "", true
		}
	}
	if foldPerson(person.FirstName, person.LastName) == "" {
		return -1, "", false
	}
	var hits []int
	for i, employee := range employees {
		sameName := keysOverlap(personKeys(employee.FirstName, employee.LastName), personKeys(person.FirstName, person.LastName))
		sameDOB := employee.DOB != nil && person.DOB != nil && *employee.DOB == *person.DOB
		incomplete := sameName && (person.DOB != nil || person.SSN != nil) &&
			(employee.DOB == nil || person.DOB == nil || sameDOB) &&
			(employee.DOB == nil || employee.SSN == nil)
		if (sameDOB && (sameName || corroboratedName(employee, person))) || incomplete {
			if employee.SSN != nil && person.SSN != nil && !sameSSNText(*employee.SSN, *person.SSN) {
				return -1, "conflicting SSN", true
			}
			hits = append(hits, i)
		}
	}
	if len(hits) == 1 {
		return hits[0], "unique compatible name and identity", false
	}
	if len(hits) > 1 {
		return -1, "", true
	}
	return -1, "", false
}

/*
matchDependent finds one dependent by SSN or a unique compatible name within the household.

* @brief: matchDependent -> finds a confident dependent of one employee
* @params: dependents [[]Dependent] -> dependents in this company
* @params: employeeID [string] -> parent employee id
* @params: person [Proposed] -> incoming dependent
* @returns: index [int] -> matching index, or -1
* @returns: how [string] -> matching evidence recorded in the MAP log
* @returns: ambiguous [bool] -> true when more than one dependent matches
*/
func matchDependent(dependents []Dependent, employeeID string, person Proposed) (int, string, bool) {
	if person.SSN != nil && len(digitsOnly(*person.SSN)) == 9 {
		var hits []int
		for i, dependent := range dependents {
			if dependent.EmployeeID == employeeID && dependent.SSN != nil && digitsOnly(*dependent.SSN) == digitsOnly(*person.SSN) {
				hits = append(hits, i)
			}
		}
		if len(hits) == 1 {
			return hits[0], "SSN", false
		}
		if len(hits) > 1 {
			return -1, "", true
		}
	}
	if foldPerson(person.FirstName, person.LastName) == "" {
		return -1, "", false
	}
	var hits []int
	for i, dependent := range dependents {
		if dependent.EmployeeID != employeeID {
			continue
		}
		if keysOverlap(personKeys(dependent.FirstName, dependent.LastName), personKeys(person.FirstName, person.LastName)) {
			if dependent.DOB != nil && person.DOB != nil && *dependent.DOB != *person.DOB {
				if dependent.SSN == nil || person.SSN == nil || sameSSNText(*dependent.SSN, *person.SSN) {
					return -1, "conflicting DOB", true
				}
				continue
			}
			if dependent.SSN != nil && person.SSN != nil && !sameSSNText(*dependent.SSN, *person.SSN) {
				return -1, "conflicting SSN", true
			}
			hits = append(hits, i)
		}
	}
	if len(hits) == 1 {
		return hits[0], "unique compatible household name", false
	}
	if len(hits) > 1 {
		return -1, "", true
	}
	return -1, "", false
}

/*
findParent returns a unique parent only when source and named owners do not disagree.

* @brief: findParent -> finds the employee a dependent belongs to
* @params: employees [[]Employee] -> employees in the next state
* @params: sources [map[string][]string] -> source ids from this document
* @params: person [Proposed] -> dependent row
* @returns: employeeID [string] -> parent employee id
* @returns: ok [bool] -> true when exactly one employee matches
*/
func findParent(employees []Employee, sources map[string][]string, person Proposed) (string, bool) {
	if person.SourceID != "" {
		ids := uniqueStrings(sources[person.SourceNamespace+"|"+strings.TrimSpace(person.SourceID)])
		if len(ids) == 1 {
			if named := employeesNamed(employees, person.Subscriber); len(named) == 1 && named[0].ID != ids[0] {
				confirmed := sources[sourceNameKey(person.SourceNamespace+"|"+strings.TrimSpace(person.SourceID), person.Subscriber)]
				if len(confirmed) != 1 || confirmed[0] != ids[0] {
					return "", false
				}
			}
			return ids[0], true
		}
		if len(ids) > 1 {
			return "", false
		}
	}
	hits := employeesNamed(employees, person.Subscriber)
	if len(hits) == 1 {
		return hits[0].ID, true
	}
	return "", false
}

/*
fillEmployee fills empty identity fields and records conflicts without overwriting.

* @brief: fillEmployee -> fills nil employee fields from a matched row
* @params: employee [*Employee] -> stored employee
* @params: person [Proposed] -> incoming employee
* @params: next [*State] -> census that receives conflict issues
* @params: decisions [*[]string] -> MAP lines
*/
func fillEmployee(employee *Employee, person Proposed, next *State, decisions *[]string) {
	if employee.FirstName == "" {
		employee.FirstName = person.FirstName
	}
	if employee.LastName == "" {
		employee.LastName = person.LastName
	}
	if employee.CompanyID == "" {
		employee.CompanyID = next.CompanyID
	}
	who := DisplayName(employee.FirstName, employee.LastName)
	setString(&employee.DOB, person.DOB, func(a, b string) bool { return a == b }, func() {
		AddIssue(next, decisions, ConflictIssue(next.CompanyID, employee.ID, "CONFLICT_DOB", who, "DOB", StringValue(employee.DOB), StringValue(person.DOB)))
	})
	setString(&employee.SSN, person.SSN, sameSSNText, func() {
		AddIssue(next, decisions, ConflictIssue(next.CompanyID, employee.ID, "CONFLICT_SSN", who, "SSN", StringValue(employee.SSN), StringValue(person.SSN)))
	})
	setString(&employee.HireDate, person.HireDate, func(a, b string) bool { return a == b }, func() {
		AddIssue(next, decisions, ConflictIssue(next.CompanyID, employee.ID, "CONFLICT_HIRE_DATE", who, "hire date", StringValue(employee.HireDate), StringValue(person.HireDate)))
	})
	setBool(&employee.EmploymentStatus, person.EmploymentStatus, func() {
		AddIssue(next, decisions, ConflictIssue(next.CompanyID, employee.ID, "CONFLICT_EMPLOYMENT", who, "employment status", boolText(employee.EmploymentStatus), boolText(person.EmploymentStatus)))
	})
}

/*
FillDependent fills empty dependent fields and records conflicts without overwriting.

* @brief: FillDependent -> fills nil dependent fields from a matched row
* @params: dependent [*Dependent] -> stored dependent
* @params: person [Proposed] -> incoming dependent
* @params: next [*State] -> census that receives conflict issues
* @params: decisions [*[]string] -> MAP lines
*/
func FillDependent(dependent *Dependent, person Proposed, next *State, decisions *[]string) {
	if dependent.FirstName == "" {
		dependent.FirstName = person.FirstName
	}
	if dependent.LastName == "" {
		dependent.LastName = person.LastName
	}
	who := DisplayName(dependent.FirstName, dependent.LastName)
	conflict := func(kind, field, stored, incoming string) {
		issue := ConflictIssue(next.CompanyID, dependent.EmployeeID, kind, who, field, stored, incoming)
		issue.Subject = dependent.ID
		AddIssue(next, decisions, issue)
	}
	setString(&dependent.DOB, person.DOB, func(a, b string) bool { return a == b }, func() {
		conflict("CONFLICT_DOB", "DOB", StringValue(dependent.DOB), StringValue(person.DOB))
	})
	setString(&dependent.SSN, person.SSN, sameSSNText, func() {
		conflict("CONFLICT_SSN", "SSN", StringValue(dependent.SSN), StringValue(person.SSN))
	})
	setString(&dependent.Relationship, person.Relationship, func(a, b string) bool { return strings.EqualFold(a, b) }, func() {
		conflict("CONFLICT_RELATIONSHIP", "relationship", StringValue(dependent.Relationship), StringValue(person.Relationship))
	})
}

/*
AddIssue appends an issue and a MAP line.

* @brief: AddIssue -> stores one issue and records it in the decision lines
* @params: next [*State] -> census being updated
* @params: decisions [*[]string] -> MAP lines
* @params: issue [OpenIssue] -> issue to store
*/
func AddIssue(next *State, decisions *[]string, issue OpenIssue) {
	if strings.TrimSpace(issue.Kind) == "" {
		issue.Kind = "UNKNOWN_VALUE"
	}
	if issue.Status == "" {
		issue.Status = "OPEN"
	}
	issue.CompanyID = next.CompanyID
	issue.EmployeeID = CloneString(issue.EmployeeID)
	issue.Claims = append([]string(nil), issue.Claims...)
	if issue.Key == "" {
		issue.Key = issueKey(issue)
	}
	for i := range next.OpenIssues {
		stored := &next.OpenIssues[i]
		if stored.Key == issue.Key || (stored.Key == "" && stored.Kind == issue.Kind && stored.Detail == issue.Detail) {
			// Repeated identical evidence does not reopen a resolved issue.
			if issue.Status == "RESOLVED" {
				stored.Status = "RESOLVED"
			}
			return
		}
	}
	if issue.ID == "" {
		id, err := CensusUUID()
		if err != nil {
			*decisions = append(*decisions, "could not create issue: "+err.Error())
			return
		}
		issue.ID = id
	}
	next.OpenIssues = append(next.OpenIssues, issue)
	*decisions = append(*decisions, strings.ToLower(issue.Status)+" "+issue.Kind+": "+issue.Detail)
}

/*
rememberSource records a source id for an employee when this document supplied one.

* @brief: rememberSource -> indexes a source id seen in this document
* @params: sources [map[string][]string] -> source id to employee ids
* @params: sourceID [string] -> worker or household reference
* @params: employeeID [string] -> employee id
*/
func rememberSource(sources map[string][]string, sourceID, employeeID string) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || employeeID == "" {
		return
	}
	for _, id := range sources[sourceID] {
		if id == employeeID {
			return
		}
	}
	sources[sourceID] = append(sources[sourceID], employeeID)
}

/*
EmployeeByID returns a display name for an employee id.

* @brief: EmployeeByID -> returns the display name of an employee
* @params: employees [[]Employee] -> employees to search
* @params: id [string] -> employee id
* @returns: name [string] -> first and last name, or the id
*/
func EmployeeByID(employees []Employee, id string) string {
	for _, employee := range employees {
		if employee.ID == id {
			return DisplayName(employee.FirstName, employee.LastName)
		}
	}
	return id
}

/*
employeeIndex returns the slice index of an employee id.

* @brief: employeeIndex -> finds an employee index by id
* @params: employees [[]Employee] -> employees to search
* @params: id [string] -> employee id
* @returns: index [int] -> index, or -1
*/
func employeeIndex(employees []Employee, id string) int {
	for i, employee := range employees {
		if employee.ID == id {
			return i
		}
	}
	return -1
}

/*
employeePtr returns the employee with an id in the current slice.

* @brief: employeePtr -> returns a pointer to an employee by id
* @params: next [*State] -> census to search
* @params: id [string] -> employee id
* @returns: employee [*Employee] -> matching employee, or nil
*/
func employeePtr(next *State, id string) *Employee {
	index := employeeIndex(next.Employees, id)
	if index < 0 {
		return nil
	}
	return &next.Employees[index]
}

/*
freshID returns a new UUID for a row created during reconciliation.

* @brief: freshID -> generates a UUID for a new census row
* @params: decisions [*[]string] -> MAP lines that receive a failure note
* @returns: id [string] -> UUID
* @returns: ok [bool] -> false when random bytes are unavailable
*/
func freshID(decisions *[]string) (string, bool) {
	id, err := CensusUUID()
	if err != nil {
		*decisions = append(*decisions, "uuid: "+err.Error())
		return "", false
	}
	return id, true
}

/*
openRowIssue builds an open issue without an id.

* @brief: openRowIssue -> builds an open issue for a format error
* @params: companyID [string] -> company id
* @params: kind [string] -> issue kind
* @params: detail [string] -> issue detail
* @returns: issue [OpenIssue] -> open issue
*/
func openRowIssue(companyID, kind, detail string) OpenIssue {
	return OpenIssue{CompanyID: companyID, Kind: kind, Detail: detail, Status: "OPEN"}
}

/*
ConflictIssue builds an open conflict issue that names both claims.

* @brief: ConflictIssue -> builds an issue that keeps both conflicting claims
* @params: companyID [string] -> company id
* @params: employeeID [string] -> employee the conflict belongs to
* @params: kind [string] -> conflict kind
* @params: who [string] -> person name
* @params: field [string] -> field that conflicts
* @params: stored [string] -> stored claim
* @params: incoming [string] -> document claim
* @returns: issue [OpenIssue] -> open conflict issue
*/
func ConflictIssue(companyID, employeeID, kind, who, field, stored, incoming string) OpenIssue {
	return OpenIssue{
		CompanyID:  companyID,
		EmployeeID: strPtr(employeeID),
		Kind:       kind,
		Subject:    employeeID,
		Field:      strings.ToLower(field),
		Claims:     []string{stored, incoming},
		Detail:     field + " conflict for " + who + ": stored " + stored + "; document " + incoming,
		Status:     "OPEN",
	}
}

/*
samePersonIssue builds the resolved same-person issue for an email.

* @brief: samePersonIssue -> builds a resolved SAME_PERSON issue
* @params: companyID [string] -> company id
* @params: employeeID [string] -> employee the resolution belongs to
* @params: resolution [*EmailResolution] -> email answer
* @returns: issue [OpenIssue] -> resolved issue
*/
func samePersonIssue(companyID, employeeID string, resolution *EmailResolution) OpenIssue {
	return OpenIssue{
		CompanyID: companyID, EmployeeID: strPtr(employeeID),
		Subject: employeeID, Field: "identity", Claims: append([]string(nil), resolution.Names...),
		Kind: "SAME_PERSON", Status: "RESOLVED",
		Detail: strings.Join(resolution.Names, " and ") + " are confirmed as the same person",
	}
}

/*
indexSources indexes persisted references without choosing between conflicting owners.

* @brief: indexSources -> builds a reference-to-owner index
* @params: employees [[]Employee] -> employees and their persisted references
* @returns: sources [map[string][]string] -> every distinct owner for each reference
*/
func indexSources(employees []Employee) map[string][]string {
	sources := map[string][]string{}
	for _, employee := range employees {
		for _, ref := range employee.SourceRefs {
			rememberSource(sources, ref.Namespace+"|"+ref.Value, employee.ID)
		}
	}
	return sources
}

/*
rememberReference retains an exact reference once without changing its namespace or value.

* @brief: rememberReference -> adds a reference absent from an employee
* @params: employee [*Employee] -> confirmed owner of the reference
* @params: ref [SourceRef] -> reference already normalized by its caller
*/
func rememberReference(employee *Employee, ref SourceRef) {
	for _, existing := range employee.SourceRefs {
		if existing == ref {
			return
		}
	}
	employee.SourceRefs = append(employee.SourceRefs, ref)
}

/*
retainUnapplied preserves a failed proposal unless an existing open issue already retains it.

* @brief: retainUnapplied -> prevents an unapplied formatted person from disappearing
* @params: next [*State] -> census containing existing review claims
* @params: person [Proposed] -> proposal whose application failed
* @params: decisions [*[]string] -> MAP decisions
*/
func retainUnapplied(next *State, person Proposed, decisions *[]string) {
	raw, _ := json.Marshal(person)
	for _, issue := range next.OpenIssues {
		if issue.Status == "RESOLVED" {
			continue
		}
		for _, claim := range issue.Claims {
			if claim == string(raw) {
				return
			}
		}
	}
	AddIssue(next, decisions, PersonReviewIssue(next.CompanyID, "UNAPPLIED_PERSON", person))
}

/*
recordSummary logs current totals and issue transitions without persisting derived counts.

* @brief: recordSummary -> appends the running tally and per-run issue changes
* @params: before [State] -> census before this document
* @params: next [State] -> reconciled census
* @params: decisions [*[]string] -> MAP lines that receive the tally
*/
func recordSummary(before, next State, decisions *[]string) {
	statuses := map[string]string{}
	for _, issue := range before.OpenIssues {
		statuses[issue.ID] = issue.Status
	}
	opened, resolved := 0, 0
	for _, issue := range next.OpenIssues {
		previous := statuses[issue.ID]
		if issue.Status == "RESOLVED" {
			if previous != "RESOLVED" {
				resolved++
			}
		} else if previous == "" || previous == "RESOLVED" {
			opened++
		}
	}
	summary, _ := json.Marshal(Summarize(next))
	*decisions = append(*decisions, fmt.Sprintf("summary: %s; issues_opened_this_run: %d; issues_resolved_this_run: %d", summary, opened, resolved))
}

/*
issueKey identifies an issue by its subject, field, and retained claims.

* @brief: issueKey -> computes the stable identity of a review issue
* @params: issue [OpenIssue] -> issue whose identifying fields are current
* @returns: key [string] -> stable issue identity independent of status
*/
func issueKey(issue OpenIssue) string {
	claims := append([]string(nil), issue.Claims...)
	sort.Strings(claims)
	subject := issue.Subject
	if subject == "" && issue.EmployeeID != nil {
		subject = *issue.EmployeeID
	}
	identity := []string{issue.CompanyID, issue.Kind, subject, issue.Field, strings.Join(claims, "|")}
	if subject == "" || (issue.Field == "" && len(claims) == 0) {
		identity = append(identity, issue.Detail)
	}
	raw, _ := json.Marshal(identity)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

/*
sourceNameKey separates document-confirmed name evidence from ordinary source-reference keys.

* @brief: sourceNameKey -> keys one accepted source-reference and literal name pair
* @params: reference [string] -> namespaced source-reference key
* @params: name [string] -> literal name confirmed by accepted identity evidence
* @returns: key [string] -> collision-free transient evidence key
*/
func sourceNameKey(reference, name string) string {
	key, _ := json.Marshal([]string{"alias", reference, foldText(strings.TrimSpace(name))})
	return string(key)
}
