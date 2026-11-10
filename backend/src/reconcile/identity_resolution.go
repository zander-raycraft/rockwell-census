package reconcile

import (
	"sort"
	"strings"
)

/*
applyEmailResolution merges a confident same-person pair or opens an ambiguous match.

* @brief: applyEmailResolution -> applies a same-person email answer
* @params: next [*State] -> census being updated
* @params: plan [Plan] -> plan that may contain an email resolution
* @params: decisions [*[]string] -> MAP lines
*/
func applyEmailResolution(next *State, plan Plan, decisions *[]string) {
	resolution := plan.EmailResolution
	if resolution == nil {
		return
	}
	if !resolution.SamePerson {
		if strings.TrimSpace(resolution.Note) != "" {
			*decisions = append(*decisions, "email resolution: not the same person")
		}
		return
	}
	matched := map[string]Employee{}
	ambiguousName := false
	for _, name := range resolution.Names {
		hits := employeesNamed(next.Employees, name)
		if len(hits) > 1 {
			ambiguousName = true
			continue
		}
		if len(hits) == 1 {
			matched[hits[0].ID] = hits[0]
		}
	}
	ids := make([]string, 0, len(matched))
	for id := range matched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if ambiguousName || len(ids) == 0 || len(ids) > 2 {
		AddIssue(next, decisions, OpenIssue{
			CompanyID: next.CompanyID,
			Kind:      "AMBIGUOUS_MATCH",
			Detail:    "email names do not identify a confident pair: " + strings.Join(resolution.Names, ", "),
			Status:    "OPEN",
		})
		return
	}
	if len(ids) == 2 {
		mergeEmployees(next, ids[0], ids[1], resolution, decisions)
		return
	}
	employee := employeePtr(next, ids[0])
	if employee == nil {
		return
	}
	applyResolutionSSN(next, employee, nil, resolution, decisions)
	applyConfirmedIdentity(next, employee.ID, resolution, decisions)
	AddIssue(next, decisions, samePersonIssue(next.CompanyID, employee.ID, resolution))
}

/*
mergeEmployees keeps the earlier employee, moves related rows, and records SSN conflicts.

* @brief: mergeEmployees -> merges two employees an email says are the same person
* @params: next [*State] -> census being updated
* @params: leftID [string] -> one employee id
* @params: rightID [string] -> the other employee id
* @params: resolution [*EmailResolution] -> same-person answer
* @params: decisions [*[]string] -> MAP lines
*/
func mergeEmployees(next *State, leftID, rightID string, resolution *EmailResolution, decisions *[]string) {
	left := employeeIndex(next.Employees, leftID)
	right := employeeIndex(next.Employees, rightID)
	if left < 0 || right < 0 {
		return
	}
	keep := left
	drop := right
	if right < left {
		keep, drop = right, left
	}
	keepID := next.Employees[keep].ID
	dropID := next.Employees[drop].ID
	dropEmployee := next.Employees[drop]
	keeper := &next.Employees[keep]
	if keeper.FirstName == "" {
		keeper.FirstName = dropEmployee.FirstName
	}
	if keeper.LastName == "" {
		keeper.LastName = dropEmployee.LastName
	}
	if keeper.CompanyID == "" {
		keeper.CompanyID = next.CompanyID
	}
	for _, ref := range dropEmployee.SourceRefs {
		rememberReference(keeper, ref)
	}
	fillEmployee(keeper, Proposed{DOB: dropEmployee.DOB, HireDate: dropEmployee.HireDate, EmploymentStatus: dropEmployee.EmploymentStatus}, next, decisions)
	applyResolutionSSN(next, keeper, &dropEmployee, resolution, decisions)
	for i := range next.Dependents {
		if next.Dependents[i].EmployeeID == dropID {
			next.Dependents[i].EmployeeID = keepID
		}
	}
	for i := range next.Enrollments {
		if next.Enrollments[i].EmployeeID == dropID {
			next.Enrollments[i].EmployeeID = keepID
		}
	}
	for i := range next.OpenIssues {
		if next.OpenIssues[i].EmployeeID != nil && *next.OpenIssues[i].EmployeeID == dropID {
			id := keepID
			next.OpenIssues[i].EmployeeID = &id
		}
	}
	remapIssueSubjects(next, map[string]string{dropID: keepID})
	filtered := make([]Employee, 0, len(next.Employees)-1)
	for _, employee := range next.Employees {
		if employee.ID != dropID {
			filtered = append(filtered, employee)
		}
	}
	next.Employees = filtered
	if resolution.CorrectSSN {
		applyResolutionSSN(next, employeePtr(next, keepID), nil, resolution, decisions)
	}
	dedupeDependents(next, decisions)
	dedupeEnrollments(next, decisions)
	*decisions = append(*decisions, "merged employee "+DisplayName(dropEmployee.FirstName, dropEmployee.LastName)+" into "+DisplayName(keeper.FirstName, keeper.LastName))
	applyConfirmedIdentity(next, keepID, resolution, decisions)
	AddIssue(next, decisions, samePersonIssue(next.CompanyID, keepID, resolution))
}

/*
applyResolutionSSN applies an explicit SSN correction or preserves conflicting claims.

* @brief: applyResolutionSSN -> accepts confirmed corrections and checks SSN ownership
* @params: next [*State] -> census that receives conflict issues
* @params: keeper [*Employee] -> employee that remains
* @params: other [*Employee] -> second employee, or nil when only one is stored
* @params: resolution [*EmailResolution] -> email answer that may include an SSN
* @params: decisions [*[]string] -> MAP lines
*/
func applyResolutionSSN(next *State, keeper *Employee, other *Employee, resolution *EmailResolution, decisions *[]string) {
	if resolution.CorrectSSN {
		ssn, valid := normalizeSSN(resolution.SSN)
		if !valid {
			AddIssue(next, decisions, OpenIssue{EmployeeID: strPtr(keeper.ID), Subject: keeper.ID, Kind: "INVALID_SSN", Field: "ssn", Claims: []string{resolution.SSN}, Detail: "email correction is not a 9-digit SSN"})
			return
		}
		for _, employee := range next.Employees {
			if employee.ID != keeper.ID && (other == nil || employee.ID != other.ID) && employee.SSN != nil && sameSSNText(*employee.SSN, ssn) {
				AddIssue(next, decisions, OpenIssue{EmployeeID: strPtr(keeper.ID), Subject: keeper.ID, Kind: "CONFLICT_SSN", Field: "ssn", Claims: []string{ssn, employee.ID}, Detail: "corrected SSN already belongs to a different employee"})
				return
			}
		}
		keeper.SSN = strPtr(ssn)
		for i := range next.OpenIssues {
			issue := &next.OpenIssues[i]
			if issue.EmployeeID != nil && *issue.EmployeeID == keeper.ID && issue.Subject == keeper.ID && issue.Field == "ssn" &&
				(issue.Kind == "CONFLICT_SSN" || issue.Kind == "MISSING_FIELD") {
				issue.Status = "RESOLVED"
				*decisions = append(*decisions, "resolved "+issue.Kind+" for explicit SSN correction")
			}
		}
		return
	}
	var claims []string
	addClaim := func(value *string) {
		if value == nil {
			return
		}
		text := strings.TrimSpace(*value)
		if text == "" {
			return
		}
		for _, claim := range claims {
			if sameSSNText(claim, text) {
				return
			}
		}
		claims = append(claims, text)
	}
	addClaim(keeper.SSN)
	if other != nil {
		addClaim(other.SSN)
	}
	if formatted, ok := normalizeSSN(resolution.SSN); ok {
		addClaim(strPtr(formatted))
	} else if strings.TrimSpace(resolution.SSN) != "" {
		AddIssue(next, decisions, OpenIssue{
			CompanyID:  next.CompanyID,
			EmployeeID: strPtr(keeper.ID),
			Kind:       "INVALID_SSN",
			Detail:     "email SSN " + quote(resolution.SSN) + " is not 9 digits",
			Status:     "OPEN",
		})
	}
	if len(claims) == 1 && keeper.SSN == nil {
		if formatted, ok := normalizeSSN(claims[0]); ok {
			keeper.SSN = strPtr(formatted)
		} else {
			keeper.SSN = strPtr(claims[0])
		}
		return
	}
	if len(claims) > 1 {
		if keeper.SSN == nil && other != nil && other.SSN != nil {
			keeper.SSN = CloneString(other.SSN)
		}
		who := DisplayName(keeper.FirstName, keeper.LastName)
		AddIssue(next, decisions, OpenIssue{
			CompanyID:  next.CompanyID,
			EmployeeID: strPtr(keeper.ID),
			Kind:       "CONFLICT_SSN",
			Subject:    keeper.ID,
			Field:      "ssn",
			Claims:     claims,
			Detail:     "SSN conflict for " + who + ": " + strings.Join(claims, "; "),
			Status:     "OPEN",
		})
	}
}

/*
dedupeDependents merges dependents that became the same person after an employee merge.

* @brief: dedupeDependents -> collapses duplicate dependents of one employee
* @params: next [*State] -> census being updated
* @params: decisions [*[]string] -> MAP lines
*/
func dedupeDependents(next *State, decisions *[]string) {
	var kept []Dependent
	replaced := map[string]string{}
	for _, dependent := range next.Dependents {
		found := -1
		for i, existing := range kept {
			if existing.EmployeeID == dependent.EmployeeID && sameDependent(existing, dependent) {
				found = i
				break
			}
		}
		if found < 0 {
			kept = append(kept, dependent)
			continue
		}
		FillDependent(&kept[found], proposedFromDependent(dependent), next, decisions)
		if dependent.ID != "" && dependent.ID != kept[found].ID {
			replaced[dependent.ID] = kept[found].ID
		}
	}
	next.Dependents = kept
	remapIssueSubjects(next, replaced)
	for i := range next.Enrollments {
		if next.Enrollments[i].DependentID == nil {
			continue
		}
		if id, ok := replaced[*next.Enrollments[i].DependentID]; ok {
			idCopy := id
			next.Enrollments[i].DependentID = &idCopy
		}
	}
}

/*
sameDependent reports whether two dependents are the same person under one employee.

* @brief: sameDependent -> compares two dependents by SSN or by name and date of birth
* @params: a [Dependent] -> first dependent
* @params: b [Dependent] -> second dependent
* @returns: same [bool] -> true when the identity rules say they are one person
*/
func sameDependent(a, b Dependent) bool {
	if a.SSN != nil && b.SSN != nil {
		da, db := digitsOnly(*a.SSN), digitsOnly(*b.SSN)
		if len(da) == 9 && da == db {
			return true
		}
		if len(da) == 9 && len(db) == 9 && da != db {
			return false
		}
	}
	if a.DOB == nil || b.DOB == nil || *a.DOB != *b.DOB {
		return false
	}
	return keysOverlap(personKeys(a.FirstName, a.LastName), personKeys(b.FirstName, b.LastName))
}

/*
proposedFromDependent wraps a stored dependent as a proposed row for field filling.

* @brief: proposedFromDependent -> copies a dependent into a proposed person
* @params: dependent [Dependent] -> stored dependent
* @returns: person [Proposed] -> fields used to fill another dependent
*/
func proposedFromDependent(dependent Dependent) Proposed {
	return Proposed{
		FirstName:    dependent.FirstName,
		LastName:     dependent.LastName,
		DOB:          CloneString(dependent.DOB),
		SSN:          CloneString(dependent.SSN),
		Relationship: CloneString(dependent.Relationship),
	}
}

/*
remapIssueSubjects keeps issue identities attached to surviving census records.

* @brief: remapIssueSubjects -> redirects subjects removed by a person or enrollment merge
* @params: next [*State] -> census with merged records and retained issues
* @params: replaced [map[string]string] -> removed record ids and their surviving ids
*/
func remapIssueSubjects(next *State, replaced map[string]string) {
	if len(replaced) == 0 {
		return
	}
	for i := range next.OpenIssues {
		issue := &next.OpenIssues[i]
		if subject, ok := replaced[issue.Subject]; ok {
			issue.Subject = subject
			issue.Key = issueKey(*issue)
		}
	}
	kept := next.OpenIssues[:0]
	seen := map[string]int{}
	for _, issue := range next.OpenIssues {
		if index, ok := seen[issue.Key]; ok && issue.Key != "" {
			if issue.Status == "OPEN" {
				kept[index].Status = "OPEN"
			}
			continue
		}
		seen[issue.Key] = len(kept)
		kept = append(kept, issue)
	}
	next.OpenIssues = kept
}
