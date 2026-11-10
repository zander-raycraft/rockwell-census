package reconcile

import (
	"encoding/json"
	"strings"
)

/*
attachEnrollments adds or merges enrollments for one person.

* @brief: attachEnrollments -> stores enrollments for an employee or dependent
* @params: next [*State] -> census being updated
* @params: employeeID [string] -> parent employee id
* @params: dependentID [*string] -> dependent id, or nil for the employee
* @params: enrollments [[]Enrollment] -> incoming enrollments
* @params: who [string] -> person name used in decision lines
* @params: decisions [*[]string] -> MAP lines
*/
func attachEnrollments(next *State, employeeID string, dependentID *string, enrollments []Enrollment, who string, decisions *[]string) {
	for _, incoming := range enrollments {
		incoming = cloneEnrollment(incoming)
		incoming.EmployeeID = employeeID
		incoming.EnrollmentType = strings.ToUpper(strings.TrimSpace(incoming.EnrollmentType))
		if incoming.EnrollmentType == "" {
			continue
		}
		if dependentID == nil {
			incoming.DependentID = nil
		} else {
			incoming.DependentID = CloneString(dependentID)
		}
		if index := findEnrollment(next.Enrollments, incoming); index >= 0 {
			mergeEnrollment(&next.Enrollments[index], incoming, next, decisions, who)
			continue
		} else if index == -2 {
			raw, _ := json.Marshal(incoming)
			subject := employeeID
			if dependentID != nil {
				subject = *dependentID
			}
			AddIssue(next, decisions, OpenIssue{EmployeeID: strPtr(employeeID), Subject: subject, Kind: "AMBIGUOUS_COVERAGE_PERIOD", Field: incoming.EnrollmentType, Claims: []string{string(raw)}, Detail: "coverage without a unique period for " + who, Status: "OPEN"})
			continue
		}
		id, ok := freshID(decisions)
		if !ok {
			continue
		}
		incoming.ID = id
		next.Enrollments = append(next.Enrollments, incoming)
		*decisions = append(*decisions, "added enrollment "+incoming.EnrollmentType+" for "+who)
	}
}

/*
findEnrollment finds an existing enrollment of the same type, person, and coverage period.

* @brief: findEnrollment -> finds the enrollment a new election should update
* @params: enrollments [[]Enrollment] -> stored enrollments
* @params: incoming [Enrollment] -> enrollment to place
* @returns: index [int] -> matching index, -1 for a new period, or -2 for ambiguous periods
*/
func findEnrollment(enrollments []Enrollment, incoming Enrollment) int {
	var same []int
	var undated []int
	var anchored []int
	for i, stored := range enrollments {
		if stored.EmployeeID != incoming.EmployeeID || stored.EnrollmentType != incoming.EnrollmentType {
			continue
		}
		if !sameStringPtr(stored.DependentID, incoming.DependentID) {
			continue
		}
		if sameStringPtr(stored.EffectiveStart, incoming.EffectiveStart) && sameStringPtr(stored.EffectiveEnd, incoming.EffectiveEnd) {
			return i
		}
		// A shared start identifies an election even when an end is later supplied.
		if stored.EffectiveStart != nil && incoming.EffectiveStart != nil && *stored.EffectiveStart == *incoming.EffectiveStart {
			anchored = append(anchored, i)
		} else if incoming.EffectiveStart == nil && stored.EffectiveEnd != nil && incoming.EffectiveEnd != nil && *stored.EffectiveEnd == *incoming.EffectiveEnd {
			anchored = append(anchored, i)
		}
		same = append(same, i)
		if stored.EffectiveStart == nil && stored.EffectiveEnd == nil {
			undated = append(undated, i)
		}
	}
	if len(anchored) == 1 {
		return anchored[0]
	}
	if len(anchored) > 1 {
		return -2
	}
	incomingDated := incoming.EffectiveStart != nil || incoming.EffectiveEnd != nil
	if incomingDated {
		var compatible []int
		for _, index := range undated {
			if enrollmentCompatible(enrollments[index], incoming) {
				compatible = append(compatible, index)
			}
		}
		if len(compatible) == 1 {
			return compatible[0]
		}
		if len(compatible) > 1 {
			return -2
		}
		return -1
	}
	var compatible []int
	for _, index := range same {
		if enrollmentCompatible(enrollments[index], incoming) {
			compatible = append(compatible, index)
		}
	}
	if len(compatible) == 1 {
		return compatible[0]
	}
	if len(same) > 0 {
		return -2
	}
	return -1
}

/*
mergeEnrollment fills empty enrollment fields and keeps conflicting stored values.

* @brief: mergeEnrollment -> fills one stored enrollment from an incoming election
* @params: stored [*Enrollment] -> enrollment already in state
* @params: incoming [Enrollment] -> election from this document
* @params: next [*State] -> census that receives conflict issues
* @params: decisions [*[]string] -> MAP lines
* @params: who [string] -> person name used in the issue
*/
func mergeEnrollment(stored *Enrollment, incoming Enrollment, next *State, decisions *[]string, who string) {
	mergeField := func(dst **string, src *string, label string) {
		setString(dst, src, func(a, b string) bool { return a == b }, func() {
			issue := ConflictIssue(next.CompanyID, stored.EmployeeID, "CONFLICT_ENROLLMENT", who, stored.EnrollmentType+" "+label, StringValue(*dst), StringValue(src))
			if stored.DependentID != nil {
				issue.Subject = *stored.DependentID
			}
			issue.Claims = append(issue.Claims, "period:"+StringValue(stored.EffectiveStart))
			AddIssue(next, decisions, issue)
		})
	}
	mergeField(&stored.Plan, incoming.Plan, "plan")
	mergeField(&stored.CoverageTier, incoming.CoverageTier, "tier")
	mergeField(&stored.Status, incoming.Status, "status")
	mergeField(&stored.EffectiveStart, incoming.EffectiveStart, "effective start")
	mergeField(&stored.EffectiveEnd, incoming.EffectiveEnd, "effective end")
}

/*
enrollmentCompatible reports whether two elections can fill the same row.

* @brief: enrollmentCompatible -> reports whether plan, tier, and status agree
* @params: stored [Enrollment] -> enrollment already in state
* @params: incoming [Enrollment] -> election from this document
* @returns: ok [bool] -> true when neither side has a conflicting value
*/
func enrollmentCompatible(stored, incoming Enrollment) bool {
	return fieldAgrees(stored.Plan, incoming.Plan) &&
		fieldAgrees(stored.CoverageTier, incoming.CoverageTier) &&
		fieldAgrees(stored.Status, incoming.Status)
}

/*
fieldAgrees reports whether two optional strings are equal or either is empty.

* @brief: fieldAgrees -> compares two optional enrollment fields
* @params: stored [*string] -> stored value
* @params: incoming [*string] -> incoming value
* @returns: ok [bool] -> true when the values do not conflict
*/
func fieldAgrees(stored, incoming *string) bool {
	if stored == nil || incoming == nil {
		return true
	}
	return *stored == *incoming
}

/*
dedupeEnrollments collapses elections that refer to the same person and period.

* @brief: dedupeEnrollments -> collapses duplicate enrollments after a merge
* @params: next [*State] -> census being updated
* @params: decisions [*[]string] -> MAP lines
*/
func dedupeEnrollments(next *State, decisions *[]string) {
	var kept []Enrollment
	replaced := map[string]string{}
	for _, enrollment := range next.Enrollments {
		if index := findEnrollment(kept, enrollment); index >= 0 {
			mergeEnrollment(&kept[index], enrollment, next, decisions, enrollment.EnrollmentType)
			if enrollment.ID != "" && enrollment.ID != kept[index].ID {
				replaced[enrollment.ID] = kept[index].ID
			}
			continue
		}
		kept = append(kept, enrollment)
	}
	next.Enrollments = kept
	remapIssueSubjects(next, replaced)
}
