package reconcile

import (
	"encoding/json"
	"strings"
	"unicode"
)

/*
corroboratedName recognizes limited spelling variants only alongside an exact DOB.

* @brief: corroboratedName -> compares literal name variants without a nickname dictionary
* @params: employee [Employee] -> candidate whose DOB must agree
* @params: person [Proposed] -> incoming identity
* @returns: match [bool] -> true for a compound surname, prefix, or one-character spelling variant
*/
func corroboratedName(employee Employee, person Proposed) bool {
	if employee.DOB == nil || person.DOB == nil || *employee.DOB != *person.DOB {
		return false
	}
	a, b := normKey(employee.FirstName), normKey(person.FirstName)
	x, y := normKey(employee.LastName), normKey(person.LastName)
	if a == "" || b == "" || x == "" || y == "" {
		return false
	}
	if a == b && x != y {
		for _, names := range [][2]string{{employee.LastName, person.LastName}, {person.LastName, employee.LastName}} {
			parts := strings.FieldsFunc(names[0], func(r rune) bool { return r == '-' || unicode.IsSpace(r) })
			if len(parts) > 1 {
				for _, part := range parts {
					if normKey(part) == normKey(names[1]) {
						return true
					}
				}
			}
		}
	}
	if x != y || len([]rune(a)) < 3 || len([]rune(b)) < 3 {
		return false
	}
	if strings.HasPrefix(a, b) || strings.HasPrefix(b, a) {
		return true
	}
	left, right := []rune(a), []rune(b)
	if len(left) > len(right) {
		left, right = right, left
	}
	if len(right)-len(left) > 1 {
		return false
	}
	i, j, differences := 0, 0, 0
	for i < len(left) && j < len(right) {
		if left[i] == right[j] {
			i++
			j++
			continue
		}
		differences++
		if differences > 1 {
			return false
		}
		if len(left) == len(right) {
			i++
		}
		j++
	}
	return differences+len(right)-j <= 1
}

/*
applyHeldClaims retries retained facts only after current evidence identifies their owner.

* @brief: applyHeldClaims -> fulfills held identity and household claims without guessing aliases
* @params: next [*State] -> census and structured claims after this document
* @params: acceptedSources [map[string][]string] -> current document references and confirmed name evidence
* @params: decisions [*[]string] -> MAP decisions for facts applied and issues resolved
*/
func applyHeldClaims(next *State, acceptedSources map[string][]string, decisions *[]string) {
	sources := indexSources(next.Employees)
	for key, ids := range acceptedSources {
		if strings.HasPrefix(key, `["alias",`) {
			for _, id := range ids {
				if employeeIndex(next.Employees, id) >= 0 {
					rememberSource(sources, key, id)
				}
			}
		}
	}
	count := len(next.OpenIssues)
	for i := 0; i < count; i++ {
		issue := next.OpenIssues[i]
		if issue.Status == "RESOLVED" || len(issue.Claims) != 1 {
			continue
		}
		if issue.Kind != "POSSIBLE_MATCH" && issue.Kind != "INSUFFICIENT_IDENTITY" && issue.Kind != "AMBIGUOUS_MATCH" && issue.Kind != "UNMATCHED_DEPENDENT" {
			continue
		}
		var person Proposed
		if json.Unmarshal([]byte(issue.Claims[0]), &person) != nil {
			continue
		}
		if person.SourceNamespace == "" {
			person.SourceNamespace = SourceReference(person.SourceID).Namespace
		}
		if person.Role == "dependent" {
			parentID, ok := findParent(next.Employees, sources, person)
			if !ok {
				continue
			}
			_, _, ambiguous := matchDependent(next.Dependents, parentID, person)
			if ambiguous {
				continue
			}
			if !applyDependent(next, person, sources, decisions) {
				continue
			}
		} else if person.Role == "employee" {
			index, _, ambiguous := matchEmployee(next.Employees, person)
			if ambiguous {
				continue
			}
			if person.SourceNamespace != "" && person.SourceID != "" {
				ids := uniqueStrings(sources[person.SourceNamespace+"|"+person.SourceID])
				if len(ids) > 1 {
					continue
				}
				if len(ids) == 1 {
					linked := employeeIndex(next.Employees, ids[0])
					if linked < 0 || (index >= 0 && linked != index) {
						continue
					}
					stored := next.Employees[linked]
					if stored.SSN != nil && person.SSN != nil && !sameSSNText(*stored.SSN, *person.SSN) {
						continue
					}
					index = linked
				}
			}
			if index < 0 {
				continue
			}
			if !applyEmployee(next, person, sources, decisions) {
				continue
			}
		} else {
			continue
		}
		next.OpenIssues[i].Status = "RESOLVED"
		*decisions = append(*decisions, "resolved "+issue.Kind+" after identity evidence became available")
	}
}

/*
SourceReference retains a namespace only when the identifier supplies a literal nonnumeric prefix.

* @brief: SourceReference -> separates a prefixed identifier without guessing its source system
* @params: value [string] -> source identifier with leading zeros preserved
* @returns: ref [SourceRef] -> prefixed reference; empty namespace for opaque identifiers
*/
func SourceReference(value string) SourceRef {
	value = strings.TrimSpace(value)
	prefix := strings.TrimRightFunc(value, unicode.IsDigit)
	hasLetter := strings.ContainsFunc(prefix, unicode.IsLetter)
	if prefix == value || !hasLetter {
		return SourceRef{Value: value}
	}
	return SourceRef{Namespace: "prefix:" + strings.ToLower(prefix), Value: value}
}

/*
PersonReviewIssue preserves the full proposed claim when it cannot be applied confidently.

* @brief: PersonReviewIssue -> retains identity and coverage evidence for review
* @params: companyID [string] -> company receiving the issue
* @params: kind [string] -> reason the proposed facts were held
* @params: person [Proposed] -> original proposed identity and benefits
* @returns: issue [OpenIssue] -> stable structured review claim
*/
func PersonReviewIssue(companyID, kind string, person Proposed) OpenIssue {
	raw, _ := json.Marshal(person)
	subject := person.SourceNamespace + "|" + person.SourceID + "|" + foldPerson(person.FirstName, person.LastName)
	return OpenIssue{CompanyID: companyID, Kind: kind, Subject: subject, Field: "identity", Claims: []string{string(raw)}, Detail: kind + ": review " + DisplayName(person.FirstName, person.LastName), Status: "OPEN"}
}

/*
refreshMissingIssues opens or resolves only structured missing-field issues against current facts.

* @brief: refreshMissingIssues -> reassesses missing identity fields without closing unrelated conflicts
* @params: next [*State] -> reconciled census with current facts
* @params: decisions [*[]string] -> changes recorded in the MAP log
*/
func refreshMissingIssues(next *State, decisions *[]string) {
	for _, employee := range next.Employees {
		fields := []struct {
			name  string
			value *string
		}{{"dob", employee.DOB}, {"ssn", employee.SSN}}
		for _, field := range fields {
			if field.value == nil {
				AddIssue(next, decisions, OpenIssue{CompanyID: next.CompanyID, EmployeeID: strPtr(employee.ID), Subject: employee.ID, Kind: "MISSING_FIELD", Field: field.name, Detail: DisplayName(employee.FirstName, employee.LastName) + ": missing " + field.name, Status: "OPEN"})
			}
		}
	}
	for _, dependent := range next.Dependents {
		if dependent.Relationship == nil || strings.TrimSpace(*dependent.Relationship) == "" {
			AddIssue(next, decisions, OpenIssue{CompanyID: next.CompanyID, EmployeeID: strPtr(dependent.EmployeeID), Subject: dependent.ID, Kind: "MISSING_FIELD", Field: "relationship", Detail: DisplayName(dependent.FirstName, dependent.LastName) + ": missing relationship", Status: "OPEN"})
		}
	}
	for i := range next.OpenIssues {
		issue := &next.OpenIssues[i]
		if issue.Kind != "MISSING_FIELD" || issue.Status == "RESOLVED" || issue.EmployeeID == nil {
			continue
		}
		if issue.Field == "relationship" {
			for _, dependent := range next.Dependents {
				if dependent.ID == issue.Subject && dependent.EmployeeID == *issue.EmployeeID && dependent.Relationship != nil && strings.TrimSpace(*dependent.Relationship) != "" {
					issue.Status = "RESOLVED"
					*decisions = append(*decisions, "resolved missing relationship for "+DisplayName(dependent.FirstName, dependent.LastName))
				}
			}
			continue
		}
		employee := employeePtr(next, *issue.EmployeeID)
		if employee == nil {
			continue
		}
		present := (issue.Field == "dob" && employee.DOB != nil) || (issue.Field == "ssn" && employee.SSN != nil)
		if present {
			issue.Status = "RESOLVED"
			*decisions = append(*decisions, "resolved missing "+issue.Field+" for "+DisplayName(employee.FirstName, employee.LastName))
		}
	}
}

/*
applyConfirmedIdentity applies held employee facts only when HR explicitly confirms that identity.

* @brief: applyConfirmedIdentity -> resolves structured identity review claims without touching unrelated issues
* @params: next [*State] -> census containing held identity claims
* @params: employeeID [string] -> confirmed surviving employee
* @params: resolution [*EmailResolution] -> explicit same-person confirmation
* @params: decisions [*[]string] -> applied facts and resolutions for the MAP log
*/
func applyConfirmedIdentity(next *State, employeeID string, resolution *EmailResolution, decisions *[]string) {
	employee := employeePtr(next, employeeID)
	if employee == nil {
		return
	}
	if resolution.CorrectSSN {
		ssn, valid := normalizeSSN(resolution.SSN)
		if !valid || employee.SSN == nil || !sameSSNText(ssn, *employee.SSN) {
			return
		}
		for _, other := range next.Employees {
			if other.ID != employeeID && other.SSN != nil && sameSSNText(*other.SSN, ssn) {
				return
			}
		}
	}
	// A name confirmation must not select among multiple held identities.
	groups := map[string]map[string]bool{}
	type heldClaim struct {
		issueIndex int
		person     Proposed
	}
	claims := []heldClaim{}
	for i, issue := range next.OpenIssues {
		if issue.Status == "RESOLVED" || (issue.Kind != "POSSIBLE_MATCH" && issue.Kind != "INSUFFICIENT_IDENTITY" && issue.Kind != "AMBIGUOUS_MATCH") || len(issue.Claims) != 1 {
			continue
		}
		var person Proposed
		if json.Unmarshal([]byte(issue.Claims[0]), &person) != nil || person.Role != "employee" {
			continue
		}
		named := false
		for _, name := range resolution.Names {
			named = named || samePersonText(name, DisplayName(person.FirstName, person.LastName))
		}
		if !named {
			continue
		}
		key := person.SourceNamespace + "|" + person.SourceID
		if person.SourceID == "" {
			key = foldPerson(person.FirstName, person.LastName) + "|" + StringValue(person.DOB) + "|" + StringValue(person.SSN)
		}
		nameKey := foldPerson(person.FirstName, person.LastName)
		if groups[nameKey] == nil {
			groups[nameKey] = map[string]bool{}
		}
		groups[nameKey][key] = true
		claims = append(claims, heldClaim{issueIndex: i, person: person})
	}
	for _, claim := range claims {
		i, person := claim.issueIndex, claim.person
		issue := next.OpenIssues[i]
		if issue.Status == "RESOLVED" || len(groups[foldPerson(person.FirstName, person.LastName)]) != 1 || (person.DOB != nil && employee.DOB != nil && *person.DOB != *employee.DOB) {
			continue
		}
		if person.SSN != nil && employee.SSN != nil && !sameSSNText(*person.SSN, *employee.SSN) && !resolution.CorrectSSN {
			continue
		}
		collision := false
		for _, other := range next.Employees {
			for _, ref := range other.SourceRefs {
				if other.ID != employeeID && ref.Namespace == person.SourceNamespace && ref.Value == person.SourceID {
					collision = true
				}
			}
		}
		if collision {
			continue
		}
		if resolution.CorrectSSN {
			person.SSN = CloneString(employee.SSN)
		}
		fillEmployee(employee, person, next, decisions)
		if person.SourceNamespace != "" && person.SourceID != "" {
			ref := SourceRef{Namespace: person.SourceNamespace, Value: person.SourceID}
			rememberReference(employee, ref)
		}
		attachEnrollments(next, employeeID, nil, person.Enrollments, DisplayName(employee.FirstName, employee.LastName), decisions)
		next.OpenIssues[i].Status = "RESOLVED"
		*decisions = append(*decisions, "resolved "+issue.Kind+" by explicit same-person confirmation")
	}
}

/*
refreshCoverageIssues checks explicit covered members without creating or changing coverage.

* @brief: refreshCoverageIssues -> flags incompatible member relationships and benefit tiers
* @params: next [*State] -> reconciled member and enrollment facts
* @params: decisions [*[]string] -> raised or resolved contradictions for the MAP log
*/
func refreshCoverageIssues(next *State, decisions *[]string) {
	dependents := map[string]Dependent{}
	for _, dependent := range next.Dependents {
		dependents[dependent.ID] = dependent
	}
	for _, enrollment := range next.Enrollments {
		if enrollment.DependentID == nil {
			continue
		}
		dependent, found := dependents[*enrollment.DependentID]
		if !found {
			continue
		}
		relationship := StringValue(dependent.Relationship)
		tier, knownTier := canonTier(StringValue(enrollment.CoverageTier))
		status := StringValue(enrollment.Status)
		knownRelationship := relationship == "SPOUSE" || relationship == "CHILD" || relationship == "OTHER"
		if status != "WAIVED" && (status != "ENROLLED" || !knownTier || !knownRelationship) {
			continue
		}
		conflict := status == "ENROLLED" && (tier == "EMPLOYEE" ||
			(tier == "EMPLOYEE_SPOUSE_ONLY" && relationship == "CHILD") ||
			(tier == "EMPLOYEE_CHILDREN" && relationship == "SPOUSE"))
		if conflict {
			issue := OpenIssue{CompanyID: next.CompanyID, EmployeeID: strPtr(enrollment.EmployeeID), Kind: "COVERAGE_RELATIONSHIP_CONFLICT", Subject: enrollment.ID, Field: "coverage_tier", Claims: []string{relationship, tier}, Detail: DisplayName(dependent.FirstName, dependent.LastName) + ": " + relationship + " has " + enrollment.EnrollmentType + " coverage under " + tier, Status: "OPEN"}
			AddIssue(next, decisions, issue)
		}
		for i := range next.OpenIssues {
			issue := &next.OpenIssues[i]
			if issue.Kind != "COVERAGE_RELATIONSHIP_CONFLICT" || issue.Subject != enrollment.ID {
				continue
			}
			current := conflict && len(issue.Claims) == 2 && issue.Claims[0] == relationship && issue.Claims[1] == tier
			if current {
				issue.Status = "OPEN"
			} else if issue.Status != "RESOLVED" {
				issue.Status = "RESOLVED"
				*decisions = append(*decisions, "resolved coverage relationship contradiction for "+DisplayName(dependent.FirstName, dependent.LastName))
			}
		}
	}
}

/*
resolveHeldMembers applies retained facts after an accepted row clarifies a member’s role or parent.

* @brief: resolveHeldMembers -> resolves supported role and parent clarifications
* @params: next [*State] -> census after current rows were applied
* @params: people [[]Proposed] -> explicit classifications supplied by this document
* @params: decisions [*[]string] -> applied facts and resolved review decisions
*/
func resolveHeldMembers(next *State, people []Proposed, decisions *[]string) {
	sources := indexSources(next.Employees)
	count := len(next.OpenIssues)
	for i := 0; i < count; i++ {
		issue := next.OpenIssues[i]
		if issue.Status == "RESOLVED" || (issue.Kind != "AMBIGUOUS_ROW_ROLE" && issue.Kind != "UNMATCHED_DEPENDENT") || len(issue.Claims) != 1 {
			continue
		}
		var held Proposed
		if json.Unmarshal([]byte(issue.Claims[0]), &held) != nil ||
			(issue.Kind == "AMBIGUOUS_ROW_ROLE" && held.Role != "") ||
			(issue.Kind == "UNMATCHED_DEPENDENT" && held.Role != "dependent") {
			continue
		}
		if held.SourceNamespace == "" {
			held.SourceNamespace = SourceReference(held.SourceID).Namespace
		}
		owner, subject := uniqueClaimOwner(*next, held)
		if subject == "" {
			continue
		}
		for _, current := range people {
			if current.Role != "employee" && current.Role != "dependent" {
				continue
			}
			currentOwner, currentSubject := uniqueClaimOwner(*next, current)
			if currentOwner != owner || currentSubject != subject || (current.Role == "employee") != (subject == owner) {
				continue
			}
			if issue.Kind == "UNMATCHED_DEPENDENT" && current.Role != "dependent" {
				continue
			}
			if subject != owner {
				if issue.Kind == "UNMATCHED_DEPENDENT" {
					if held.SourceID != "" {
						ids := uniqueStrings(sources[held.SourceNamespace+"|"+strings.TrimSpace(held.SourceID)])
						if len(ids) != 1 || ids[0] != owner {
							continue
						}
					}
					if held.Subscriber != "" {
						compatible := false
						for _, employee := range employeesNamed(next.Employees, held.Subscriber) {
							compatible = compatible || employee.ID == owner
						}
						if !compatible {
							continue
						}
					}
				} else if held.SourceID != "" || held.Subscriber != "" {
					parent, ok := findParent(next.Employees, sources, held)
					if !ok || parent != owner {
						continue
					}
				}
				// Employment facts cannot be assigned to a dependent or silently discarded.
				if held.EmploymentStatus != nil || held.HireDate != nil {
					continue
				}
				for j := range next.Dependents {
					if next.Dependents[j].ID == subject {
						FillDependent(&next.Dependents[j], held, next, decisions)
					}
				}
				attachEnrollments(next, owner, strPtr(subject), held.Enrollments, DisplayName(held.FirstName, held.LastName), decisions)
			} else {
				if held.Relationship != nil {
					continue
				}
				if held.Subscriber != "" && !samePersonText(held.Subscriber, EmployeeByID(next.Employees, owner)) {
					continue
				}
				fillEmployee(employeePtr(next, owner), held, next, decisions)
				attachEnrollments(next, owner, nil, held.Enrollments, DisplayName(held.FirstName, held.LastName), decisions)
			}
			next.OpenIssues[i].EmployeeID = strPtr(owner)
			next.OpenIssues[i].Subject = subject
			next.OpenIssues[i].Status = "RESOLVED"
			*decisions = append(*decisions, "resolved "+issue.Kind+" after explicit member clarification")
			break
		}
	}
}

/*
uniqueClaimOwner requires compatible identifiers and unique evidence across both member tables.

* @brief: uniqueClaimOwner -> identifies a retained claim without assuming its role
* @params: state [State] -> employees and dependents to compare
* @params: person [Proposed] -> identity whose role may be unknown
* @returns: owner [string] -> parent employee id, empty if no unique match
* @returns: subject [string] -> matching employee or dependent id
*/
func uniqueClaimOwner(state State, person Proposed) (owner, subject string) {
	hits := 0
	compatible := func(first, last string, dob, ssn *string) bool {
		if dob != nil && person.DOB != nil && *dob != *person.DOB || ssn != nil && person.SSN != nil && !sameSSNText(*ssn, *person.SSN) {
			return false
		}
		return ssn != nil && person.SSN != nil && sameSSNText(*ssn, *person.SSN) ||
			dob != nil && person.DOB != nil && *dob == *person.DOB && foldPerson(first, last) == foldPerson(person.FirstName, person.LastName) && foldPerson(first, last) != ""
	}
	for _, employee := range state.Employees {
		match := compatible(employee.FirstName, employee.LastName, employee.DOB, employee.SSN)
		if !match && person.SourceID != "" && (person.Role == "employee" || person.Role == "") && foldPerson(employee.FirstName, employee.LastName) == foldPerson(person.FirstName, person.LastName) {
			ref := SourceRef{Namespace: person.SourceNamespace, Value: person.SourceID}
			if ref.Namespace == "" {
				ref = SourceReference(person.SourceID)
			}
			for _, stored := range employee.SourceRefs {
				if stored == ref && (employee.DOB == nil || person.DOB == nil || *employee.DOB == *person.DOB) && (employee.SSN == nil || person.SSN == nil || sameSSNText(*employee.SSN, *person.SSN)) {
					match = true
				}
			}
		}
		if match {
			hits++
			owner, subject = employee.ID, employee.ID
		}
	}
	for _, dependent := range state.Dependents {
		if compatible(dependent.FirstName, dependent.LastName, dependent.DOB, dependent.SSN) {
			hits++
			owner, subject = dependent.EmployeeID, dependent.ID
		}
	}
	if hits != 1 {
		return "", ""
	}
	return owner, subject
}
