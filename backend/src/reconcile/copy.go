package reconcile

/*
CopyState deep-copies census slices so the caller cannot mutate the next state.

* @brief: CopyState -> copies employees, dependents, enrollments, and issues
* @params: in [State] -> state to copy
* @returns: out [State] -> independent copy
*/
func CopyState(in State) State {
	out := in
	out.Employees = copyEmployees(in.Employees)
	out.Dependents = copyDependents(in.Dependents)
	out.Enrollments = copyEnrollments(in.Enrollments)
	out.OpenIssues = copyIssues(in.OpenIssues)
	return out
}

/*
copyEmployees copies the employee slice and its pointer fields.

* @brief: copyEmployees -> deep-copies employees
* @params: in [[]Employee] -> employees to copy
* @returns: out [[]Employee] -> independent employee slice
*/
func copyEmployees(in []Employee) []Employee {
	out := make([]Employee, 0, len(in))
	for _, employee := range in {
		employee.DOB = CloneString(employee.DOB)
		employee.SSN = CloneString(employee.SSN)
		employee.EmploymentStatus = cloneBool(employee.EmploymentStatus)
		employee.HireDate = CloneString(employee.HireDate)
		employee.SourceRefs = append([]SourceRef(nil), employee.SourceRefs...)
		out = append(out, employee)
	}
	return out
}

/*
copyDependents copies the dependent slice and its pointer fields.

* @brief: copyDependents -> deep-copies dependents
* @params: in [[]Dependent] -> dependents to copy
* @returns: out [[]Dependent] -> independent dependent slice
*/
func copyDependents(in []Dependent) []Dependent {
	out := make([]Dependent, 0, len(in))
	for _, dependent := range in {
		dependent.DOB = CloneString(dependent.DOB)
		dependent.SSN = CloneString(dependent.SSN)
		dependent.Relationship = CloneString(dependent.Relationship)
		out = append(out, dependent)
	}
	return out
}

/*
copyEnrollments copies the enrollment slice and its pointer fields.

* @brief: copyEnrollments -> deep-copies enrollments
* @params: in [[]Enrollment] -> enrollments to copy
* @returns: out [[]Enrollment] -> independent enrollment slice
*/
func copyEnrollments(in []Enrollment) []Enrollment {
	out := make([]Enrollment, 0, len(in))
	for _, enrollment := range in {
		out = append(out, cloneEnrollment(enrollment))
	}
	return out
}

/*
copyIssues copies the issue slice and its employee pointer.

* @brief: copyIssues -> deep-copies open issues
* @params: in [[]OpenIssue] -> issues to copy
* @returns: out [[]OpenIssue] -> independent issue slice
*/
func copyIssues(in []OpenIssue) []OpenIssue {
	out := make([]OpenIssue, 0, len(in))
	for _, issue := range in {
		issue.EmployeeID = CloneString(issue.EmployeeID)
		issue.Claims = append([]string(nil), issue.Claims...)
		out = append(out, issue)
	}
	return out
}

/*
cloneEnrollment copies one enrollment and its optional fields.

* @brief: cloneEnrollment -> deep-copies one enrollment
* @params: in [Enrollment] -> enrollment to copy
* @returns: out [Enrollment] -> enrollment with its own pointers
*/
func cloneEnrollment(in Enrollment) Enrollment {
	in.DependentID = CloneString(in.DependentID)
	in.Plan = CloneString(in.Plan)
	in.CoverageTier = CloneString(in.CoverageTier)
	in.Status = CloneString(in.Status)
	in.EffectiveStart = CloneString(in.EffectiveStart)
	in.EffectiveEnd = CloneString(in.EffectiveEnd)
	return in
}
