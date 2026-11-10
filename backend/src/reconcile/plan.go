package reconcile

import (
	"fmt"
	"strings"
)

/*
NormalizePlan copies a plan and normalizes supported spellings without inferring column meaning.

* @brief: NormalizePlan -> canonicalizes a model plan before validation
* @params: plan [Plan] -> model output whose owned slices remain unchanged
* @params: parsed [Parsed] -> actual headers used to resolve unique case-insensitive matches
* @returns: normalized [Plan] -> independent plan with canonical fields, labels, and headers
*/
func NormalizePlan(plan Plan, parsed Parsed) Plan {
	plan.Mappings = append([]FieldMap(nil), plan.Mappings...)
	plan.Labels = append([]LabelMap(nil), plan.Labels...)
	plan.Uncertainties = append([]string(nil), plan.Uncertainties...)
	plan.DocumentType = strings.ToLower(strings.TrimSpace(plan.DocumentType))
	plan.DateFormat = strings.ToUpper(strings.TrimSpace(plan.DateFormat))
	plan.Intent = strings.ToUpper(strings.TrimSpace(plan.Intent))
	if plan.Intent == "" {
		plan.Intent = "SNAPSHOT"
		if parsed.Kind == "email" {
			plan.Intent = "NO_CHANGE"
			if plan.EmailResolution != nil {
				plan.Intent = "CORRECTION"
			}
		}
	}
	for i := range plan.Mappings {
		m := &plan.Mappings[i]
		m.Destination = strings.ToLower(strings.TrimSpace(m.Destination))
		m.Source = strings.TrimSpace(m.Source)
		m.Literal = strings.ToUpper(strings.TrimSpace(m.Literal))
		if benefit, ok := canonBenefit(m.Literal); ok {
			m.Literal = benefit
		}
		matches := 0
		var header string
		for _, candidate := range parsed.Headers {
			if strings.EqualFold(strings.TrimSpace(candidate), m.Source) {
				matches++
				header = candidate
			}
		}
		if matches == 1 {
			m.Source = header
		}
	}
	for i := range plan.Labels {
		label := &plan.Labels[i]
		label.Field = fieldGroup(label.Field)
		if label.Field == "row.role" {
			label.Field = "relationship"
		}
		label.Source = strings.TrimSpace(label.Source)
		if label.Field == "relationship" && (normKey(label.Source) == "dependent" || normKey(label.Source) == "dep") && strings.EqualFold(strings.TrimSpace(label.Value), "OTHER") {
			label.Value = "DEPENDENT"
		}
		label.Value = strings.TrimSpace(label.Value)
		if value, ok := canonicalPlanLabel(label.Field, label.Value); ok {
			label.Value = value
		}
	}
	if plan.EmailResolution != nil {
		resolution := *plan.EmailResolution
		resolution.Names = append([]string(nil), resolution.Names...)
		for i := range resolution.Names {
			resolution.Names[i] = strings.TrimSpace(resolution.Names[i])
		}
		resolution.SSN = strings.TrimSpace(resolution.SSN)
		resolution.Note = strings.TrimSpace(resolution.Note)
		plan.EmailResolution = &resolution
	}
	return plan
}

/*
ValidatePlan rejects plans whose mappings would be ignored or applied ambiguously.

* @brief: ValidatePlan -> checks the normalized extraction contract before formatting
* @params: plan [Plan] -> normalized model output
* @params: parsed [Parsed] -> document kind and available headers
* @returns: err [error] -> non-nil for unsupported, conflicting, or unusable mappings
*/
func ValidatePlan(plan Plan, parsed Parsed) error {
	switch plan.Intent {
	case "SNAPSHOT", "CHANGE_REQUEST", "NO_CHANGE", "CORRECTION":
	default:
		return fmt.Errorf("unsupported document intent %q", plan.Intent)
	}
	switch plan.DateFormat {
	case "MDY", "DMY", "ISO", "MIXED":
	default:
		return fmt.Errorf("unsupported date format %q", plan.DateFormat)
	}
	if parsed.Kind == "email" {
		if plan.Intent == "SNAPSHOT" || (plan.EmailResolution != nil && plan.Intent != "CORRECTION") {
			return fmt.Errorf("email intent does not support its resolution")
		}
		if len(plan.Mappings) != 0 || len(plan.Labels) != 0 {
			return fmt.Errorf("email plan must not contain table mappings or labels")
		}
		if r := plan.EmailResolution; r != nil {
			if r.CorrectSSN {
				if _, valid := normalizeSSN(r.SSN); !valid {
					return fmt.Errorf("explicit ssn correction requires a 9-digit ssn")
				}
			}
			if len(r.Names) == 0 || (len(r.Names) < 2 && !(r.CorrectSSN && r.SamePerson)) {
				return fmt.Errorf("email resolution needs two names or one name for an explicit same-person ssn correction")
			}
			for _, name := range r.Names {
				if name == "" {
					return fmt.Errorf("email resolution contains a blank name")
				}
			}
		}
		return nil
	}
	if parsed.Kind != "csv" || plan.EmailResolution != nil {
		return fmt.Errorf("table plan requires csv input and no email resolution")
	}
	seen := map[string]string{}
	personMapped := false
	for _, m := range plan.Mappings {
		if !knownDestination(m.Destination) {
			return fmt.Errorf("unknown destination %q", m.Destination)
		}
		matches := 0
		for _, header := range parsed.Headers {
			if m.Source != "" && strings.EqualFold(strings.TrimSpace(header), strings.TrimSpace(m.Source)) {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("source %q for %s matches %d headers; need exactly one", m.Source, m.Destination, matches)
		}
		if m.Literal != "" {
			if !strings.HasPrefix(m.Destination, "enrollment.") || m.Destination == "enrollment.type" {
				return fmt.Errorf("benefit literal is not supported for %s", m.Destination)
			}
			if _, ok := canonBenefit(m.Literal); !ok {
				return fmt.Errorf("unknown benefit literal %q", m.Literal)
			}
		}
		key := m.Destination + ":" + m.Literal
		if source, ok := seen[key]; ok && source != m.Source && m.Destination != "row.role" {
			return fmt.Errorf("conflicting sources for %s", key)
		}
		seen[key] = m.Source
		switch m.Destination {
		case "employee.first_name", "employee.last_name", "employee.full_name",
			"employee.ssn", "employee.source_id", "dependent.ssn", "dependent.subscriber_id",
			"dependent.first_name", "dependent.last_name", "dependent.full_name":
			personMapped = true
		}
	}
	if !personMapped {
		return fmt.Errorf("table plan has no person name or identifier mapping")
	}
	if err := validateIdentityMappings(plan, parsed); err != nil {
		return err
	}
	if err := validateMappedDates(plan, parsed); err != nil {
		return err
	}
	if err := ValidateLabelMeanings(plan); err != nil {
		return err
	}
	labels := map[string]string{}
	for _, label := range plan.Labels {
		if _, ok := canonicalPlanLabel(label.Field, label.Value); !ok || label.Source == "" {
			return fmt.Errorf("unsupported label %q: %q -> %q", label.Field, label.Source, label.Value)
		}
		key := label.Field + ":" + strings.ToLower(label.Source)
		if value, ok := labels[key]; ok && value != label.Value {
			return fmt.Errorf("conflicting values for label %q", key)
		}
		labels[key] = label.Value
	}
	return nil
}

/*
canonicalPlanLabel accepts only meanings supported by the formatter, without inferring unsupported meanings.

* @brief: canonicalPlanLabel -> normalizes a supported label value within its field
* @params: field [string] -> normalized label group
* @params: value [string] -> model interpretation of a raw cell
* @returns: canonical [string] -> supported canonical value, or empty when unknown
* @returns: ok [bool] -> true when the interpretation is compatible with the field
*/
func canonicalPlanLabel(field, value string) (string, bool) {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch field {
	case "type":
		return canonBenefit(value)
	case "tier":
		if tier, ok := canonTier(value); ok {
			return tier, true
		}
	case "status":
		switch value {
		case "ENROLLED", "WAIVED", "PENDING", "TERMINATED":
			return value, true
		}
	case "relationship":
		switch value {
		case "EMPLOYEE", "DEPENDENT", "SPOUSE", "CHILD", "OTHER":
			return value, true
		}
	case "employment":
		switch value {
		case "ACTIVE", "TRUE":
			return "ACTIVE", true
		case "INACTIVE", "FALSE":
			return "INACTIVE", true
		}
	}
	return "", false
}

/*
ExplicitDateFormat reads literal date legends, rejecting conflicting document conventions.

* @brief: ExplicitDateFormat -> recognizes explicit slash-date conventions without guessing
* @params: parsed [Parsed] -> skipped legends and original headers
* @returns: format [string] -> DMY, MDY, or empty when no legend is present
* @returns: err [error] -> non-nil when the document declares both conventions
*/
func ExplicitDateFormat(parsed Parsed) (string, error) {
	format := ""
	for _, text := range append(append([]string(nil), parsed.Skipped...), parsed.Headers...) {
		for legend, convention := range map[string]string{"DD/MM/YYYY": "DMY", "MM/DD/YYYY": "MDY"} {
			if strings.Contains(strings.ToUpper(text), legend) {
				if format != "" && format != convention {
					return "", fmt.Errorf("conflicting explicit date legends")
				}
				format = convention
			}
		}
	}
	return format, nil
}

/*
validateIdentityMappings prevents populated identity columns from silently disappearing from a plan.

* @brief: validateIdentityMappings -> requires recognized identity evidence to reach person fields
* @params: plan [Plan] -> normalized extraction mappings
* @params: parsed [Parsed] -> source headers and complete rows
* @returns: err [error] -> non-nil when a populated recognized identity column is omitted or misrouted
*/
func validateIdentityMappings(plan Plan, parsed Parsed) error {
	for col, header := range parsed.Headers {
		field := ""
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "dob", "member dob", "date of birth", "birth date", "birthdate", "dob (dd/mm/yyyy)":
			field = "dob"
		case "ssn", "member ssn", "social security #", "tax id", "national id":
			field = "ssn"
		}
		if field == "" {
			continue
		}
		populated := false
		for _, row := range parsed.Rows {
			if col < len(row) && strings.TrimSpace(row[col]) != "" {
				populated = true
				break
			}
		}
		if !populated {
			continue
		}
		mapped := false
		for _, mapping := range plan.Mappings {
			if mapping.Source == header && (mapping.Destination == "employee."+field || mapping.Destination == "dependent."+field) {
				mapped = true
				break
			}
		}
		if !mapped {
			return fmt.Errorf("populated identity column %q must map to employee.%s or dependent.%s", header, field, field)
		}
	}
	return nil
}

/*
ValidateLabelMeanings rejects model labels that contradict the formatter's established meanings.

* @brief: ValidateLabelMeanings -> keeps roles, tiers, statuses, and unknown negatives distinct
* @params: plan [Plan] -> normalized model labels
* @returns: err [error] -> non-nil when a label invents or contradicts supported meaning
*/
func ValidateLabelMeanings(plan Plan) error {
	for _, label := range plan.Labels {
		want := ""
		switch label.Field {
		case "relationship":
			relationship, role, known := classifyRelationship(label.Source)
			if known {
				want = relationship
				if role == "employee" {
					want = "EMPLOYEE"
				}
				if want == "" && role == "dependent" {
					want = "DEPENDENT"
				}
			}
		case "tier":
			want, _ = canonTier(label.Source)
			if _, status := canonStatusValue(label.Source); status {
				return fmt.Errorf("status label %q cannot define a coverage tier", label.Source)
			}
		case "status":
			want, _ = canonStatusValue(label.Source)
			if _, tier := canonTier(label.Source); tier {
				return fmt.Errorf("coverage tier %q does not establish enrollment status", label.Source)
			}
			switch normKey(label.Source) {
			case "n", "no", "notenrolled", "notelected", "newhirenotelected":
				return fmt.Errorf("negative participation label %q cannot imply %s", label.Source, label.Value)
			case "y", "yes":
				want = "ENROLLED"
			}
		case "type":
			want, _ = canonBenefit(label.Source)
		case "employment":
			if active, known := parseEmployment(label.Source, nil); known {
				want = "INACTIVE"
				if *active {
					want = "ACTIVE"
				}
			}
		}
		if want != "" && label.Value != want {
			return fmt.Errorf("label %s %q means %s, not %s", label.Field, label.Source, want, label.Value)
		}
	}
	return nil
}

/*
validateMappedDates checks every mapped date cell against explicit conventions and unambiguous evidence.

* @brief: validateMappedDates -> rejects date conventions contradicted outside the sampled rows
* @params: plan [Plan] -> mapped date fields and selected date convention
* @params: parsed [Parsed] -> full table and document legends
* @returns: err [error] -> non-nil for conflicting conventions; invalid dates remain row issues
*/
func validateMappedDates(plan Plan, parsed Parsed) error {
	legend, err := ExplicitDateFormat(parsed)
	if err != nil {
		return err
	}
	for _, mapping := range plan.Mappings {
		switch mapping.Destination {
		case "employee.dob", "dependent.dob", "employee.hire_date", "enrollment.effective_start", "enrollment.effective_end":
		default:
			continue
		}
		for _, row := range parsed.Rows {
			cell, _ := mappedCell(parsed.Headers, row, mapping.Source)
			if !strings.Contains(cell, "/") || looksYearFirst(cell) {
				continue
			}
			if legend != "" && plan.DateFormat != legend {
				return fmt.Errorf("date format %s conflicts with explicit %s legend for %s", plan.DateFormat, legend, mapping.Source)
			}
			_, _, mdy := parseDate(cell, "MDY")
			_, _, dmy := parseDate(cell, "DMY")
			if ((mdy || dmy) && plan.DateFormat == "ISO") || (mdy && !dmy && plan.DateFormat == "DMY") || (dmy && !mdy && plan.DateFormat != "DMY") {
				return fmt.Errorf("date format %s conflicts with unambiguous date in %s", plan.DateFormat, mapping.Source)
			}
		}
	}
	return nil
}
