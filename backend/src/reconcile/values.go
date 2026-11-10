package reconcile

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

/*
knownDestination reports whether a mapping destination is in the closed set.

* @brief: knownDestination -> checks one mapping destination
* @params: dest [string] -> destination field
* @returns: ok [bool] -> true when Format knows how to apply the destination
*/
func knownDestination(dest string) bool {
	switch strings.TrimSpace(dest) {
	case "employee.first_name", "employee.last_name", "employee.full_name",
		"employee.dob", "employee.ssn", "employee.employment_status", "employee.employment_notes", "employee.hire_date", "employee.source_id",
		"dependent.first_name", "dependent.last_name", "dependent.full_name",
		"dependent.dob", "dependent.ssn", "dependent.relationship", "dependent.subscriber_id",
		"row.role", "enrollment.type", "enrollment.coverage_tier", "enrollment.status",
		"enrollment.plan", "enrollment.effective_start", "enrollment.effective_end":
		return true
	default:
		return false
	}
}

/*
columnIndex finds a header by exact text, then by a case-insensitive trim.

* @brief: columnIndex -> finds a source header
* @params: headers [[]string] -> header cells
* @params: source [string] -> header text from the plan
* @returns: index [int] -> column index
* @returns: ok [bool] -> true when the header exists
*/
func columnIndex(headers []string, source string) (int, bool) {
	source = strings.TrimSpace(source)
	if source == "" {
		return 0, false
	}
	for i, header := range headers {
		if header == source {
			return i, true
		}
	}
	for i, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header), source) {
			return i, true
		}
	}
	return 0, false
}

/*
mappedCell returns the trimmed cell for a source header.

* @brief: mappedCell -> reads one mapped cell
* @params: headers [[]string] -> header cells
* @params: row [[]string] -> data cells
* @params: source [string] -> header text
* @returns: cell [string] -> trimmed cell, empty when blank
* @returns: ok [bool] -> true when the header exists
*/
func mappedCell(headers, row []string, source string) (string, bool) {
	index, ok := columnIndex(headers, source)
	if !ok || index >= len(row) {
		return "", false
	}
	return strings.TrimSpace(row[index]), true
}

/*
SplitName splits "Last, First" and "First Last".

* @brief: SplitName -> splits a full name into first and last
* @params: full [string] -> raw full name
* @returns: first [string] -> given name
* @returns: last [string] -> family name
*/
func SplitName(full string) (string, string) {
	full = strings.TrimSpace(full)
	if full == "" {
		return "", ""
	}
	if comma := strings.Index(full, ","); comma >= 0 {
		last := strings.TrimSpace(full[:comma])
		first := strings.TrimSpace(full[comma+1:])
		return first, last
	}
	parts := strings.Fields(full)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return strings.Join(parts[:len(parts)-1], " "), parts[len(parts)-1]
}

/*
parseDate interprets a cell with the document date format.

* @brief: parseDate -> converts one date cell to ISO
* @params: raw [string] -> date text
* @params: format [string] -> MDY, DMY, ISO, or MIXED
* @returns: value [string] -> ISO date when valid
* @returns: present [bool] -> true when the cell is non-empty
* @returns: valid [bool] -> true when the cell is a real calendar date
*/
func parseDate(raw, format string) (string, bool, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, true
	}
	format = strings.ToUpper(strings.TrimSpace(format))
	if looksYearFirst(raw) {
		format = "ISO"
	}
	var year, month, day int
	var ok bool
	switch format {
	case "ISO":
		year, month, day, ok = dateParts(raw, "ISO")
	case "DMY":
		year, month, day, ok = dateParts(raw, "DMY")
	case "MDY":
		year, month, day, ok = dateParts(raw, "MDY")
	default:
		if looksYearFirst(raw) {
			year, month, day, ok = dateParts(raw, "ISO")
		} else {
			year, month, day, ok = dateParts(raw, "MDY")
		}
	}
	if !ok || !validDate(year, month, day) {
		return "", true, false
	}
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day), true, true
}

/*
dateParts splits a date into year, month, and day for one layout.

* @brief: dateParts -> reads year, month, and day from a date string
* @params: raw [string] -> date text
* @params: layout [string] -> ISO, DMY, or MDY
* @returns: year [int] -> four-digit year
* @returns: month [int] -> month number
* @returns: day [int] -> day number
* @returns: ok [bool] -> true when the text has three numeric parts
*/
func dateParts(raw, layout string) (int, int, int, bool) {
	parts := splitDate(raw)
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	nums := [3]int{}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || len(part) > 4 {
			return 0, 0, 0, false
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0, 0, 0, false
		}
		nums[i] = n
	}
	switch layout {
	case "ISO":
		if len(strings.TrimSpace(parts[0])) != 4 {
			return 0, 0, 0, false
		}
		return nums[0], nums[1], nums[2], true
	case "DMY":
		if len(strings.TrimSpace(parts[2])) != 4 {
			return 0, 0, 0, false
		}
		return nums[2], nums[1], nums[0], true
	default:
		if len(strings.TrimSpace(parts[2])) != 4 {
			return 0, 0, 0, false
		}
		return nums[2], nums[0], nums[1], true
	}
}

/*
splitDate splits a date on slashes, dashes, or dots.

* @brief: splitDate -> splits a date into parts
* @params: raw [string] -> date text
* @returns: parts [[]string] -> separator-delimited parts
*/
func splitDate(raw string) []string {
	switch {
	case strings.Contains(raw, "/"):
		return strings.Split(raw, "/")
	case strings.Contains(raw, "-"):
		return strings.Split(raw, "-")
	case strings.Contains(raw, "."):
		return strings.Split(raw, ".")
	default:
		return nil
	}
}

/*
looksYearFirst reports whether a date starts with a four-digit year.

* @brief: looksYearFirst -> detects an ISO-shaped date
* @params: raw [string] -> date text
* @returns: ok [bool] -> true when the first part is a four-digit year
*/
func looksYearFirst(raw string) bool {
	parts := splitDate(raw)
	if len(parts) != 3 {
		return false
	}
	year := strings.TrimSpace(parts[0])
	if len(year) != 4 {
		return false
	}
	_, err := strconv.Atoi(year)
	return err == nil
}

/*
validDate reports whether a year, month, and day exist on the calendar.

* @brief: validDate -> checks a calendar date
* @params: year [int] -> year
* @params: month [int] -> month
* @params: day [int] -> day
* @returns: ok [bool] -> true when the date exists
*/
func validDate(year, month, day int) bool {
	if year < 1 || month < 1 || month > 12 || day < 1 {
		return false
	}
	stamp := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return stamp.Year() == year && int(stamp.Month()) == month && stamp.Day() == day
}

/*
normalizeSSN formats nine digits as XXX-XX-XXXX.

* @brief: normalizeSSN -> canonicalizes a Social Security number
* @params: raw [string] -> SSN text
* @returns: formatted [string] -> formatted number, or the trimmed text
* @returns: nine [bool] -> true when the cell contains nine digits
*/
func normalizeSSN(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	digits := digitsOnly(trimmed)
	if len(digits) != 9 {
		return trimmed, false
	}
	return digits[0:3] + "-" + digits[3:5] + "-" + digits[5:9], true
}

/*
digitsOnly returns the decimal digits in text.

* @brief: digitsOnly -> keeps digits from a cell
* @params: raw [string] -> input text
* @returns: digits [string] -> digits in their original order
*/
func digitsOnly(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

/*
isWaiverText reports whether a cell explicitly waives or declines coverage.

* @brief: isWaiverText -> detects waive, waived, and decline
* @params: raw [string] -> cell text
* @returns: waiver [bool] -> true when the cell is an explicit waiver
*/
func isWaiverText(raw string) bool {
	key := normKey(raw)
	return strings.Contains(key, "waive") || strings.Contains(key, "decline")
}

/*
normKey lowercases text and keeps letters and digits.

* @brief: normKey -> builds a comparison key
* @params: raw [string] -> input text
* @returns: key [string] -> lowercase letters and digits
*/
func normKey(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(raw) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

/*
canonTier maps a coverage label to a canonical tier.

* @brief: canonTier -> canonicalizes a coverage tier
* @params: raw [string] -> tier text or label value
* @returns: tier [string] -> EMPLOYEE, EMPLOYEE_SPOUSE_ONLY, EMPLOYEE_CHILDREN, or EMPLOYEE_FAMILY
* @returns: ok [bool] -> true when the tier is known
*/
func canonTier(raw string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "EMPLOYEE", "EMPLOYEE_SPOUSE_ONLY", "EMPLOYEE_CHILDREN", "EMPLOYEE_FAMILY":
		return strings.ToUpper(strings.TrimSpace(raw)), true
	}
	tier, ok := tierByKey[normKey(raw)]
	return tier, ok
}

/*
canonStatusValue maps an enrollment status label to a canonical status.

* @brief: canonStatusValue -> canonicalizes an enrollment status
* @params: raw [string] -> status text or label value
* @returns: status [string] -> ENROLLED, WAIVED, PENDING, or TERMINATED
* @returns: ok [bool] -> true when the status is known
*/
func canonStatusValue(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return "", false
	}
	if isWaiverText(raw) {
		return "WAIVED", true
	}
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "ENROLLED", "WAIVED", "PENDING", "TERMINATED":
		return strings.ToUpper(strings.TrimSpace(raw)), true
	}
	switch normKey(raw) {
	case "enrolled", "active", "y", "yes":
		return "ENROLLED", true
	case "pending":
		return "PENDING", true
	case "terminated", "termed":
		return "TERMINATED", true
	default:
		return "", false
	}
}

/*
canonBenefit maps a benefit name to MEDICAL, DENTAL, or VISION.

* @brief: canonBenefit -> canonicalizes a benefit type
* @params: raw [string] -> benefit text
* @returns: benefit [string] -> MEDICAL, DENTAL, or VISION
* @returns: ok [bool] -> true when the benefit is known
*/
func canonBenefit(raw string) (string, bool) {
	switch normKey(raw) {
	case "medical", "med":
		return "MEDICAL", true
	case "dental", "den":
		return "DENTAL", true
	case "vision", "vis":
		return "VISION", true
	default:
		return "", false
	}
}

/*
benefitLiteral returns a canonical benefit literal or empty when the literal is not a benefit.

* @brief: benefitLiteral -> normalizes a mapping literal
* @params: raw [string] -> literal from the plan
* @returns: benefit [string] -> MEDICAL, DENTAL, VISION, or empty
*/
func benefitLiteral(raw string) string {
	benefit, ok := canonBenefit(raw)
	if !ok {
		return ""
	}
	return benefit
}

/*
classifyRelationship reads a relationship cell as an employee code or a dependent relationship.

* @brief: classifyRelationship -> classifies a relationship cell
* @params: raw [string] -> relationship text
* @returns: relationship [string] -> SPOUSE, CHILD, OTHER, or empty
* @returns: role [string] -> employee or dependent
* @returns: known [bool] -> true when the cell is a known role or relationship
*/
func classifyRelationship(raw string) (string, string, bool) {
	switch normKey(raw) {
	case "employee", "ee", "subscriber", "self", "sub":
		return "", "employee", true
	case "spouse", "sp", "husband", "wife":
		return "SPOUSE", "dependent", true
	case "child", "ch", "son", "daughter", "dtr", "kid":
		return "CHILD", "dependent", true
	case "other":
		return "OTHER", "dependent", true
	case "dependent", "dep":
		return "", "dependent", true
	default:
		switch strings.ToUpper(strings.TrimSpace(raw)) {
		case "SPOUSE", "CHILD", "OTHER":
			return strings.ToUpper(strings.TrimSpace(raw)), "dependent", true
		default:
			return "", "", false
		}
	}
}

/*
labeled returns a label value for a field and cell, or the cell itself.

* @brief: labeled -> applies a label map to one cell
* @params: labels [[]LabelMap] -> plan labels
* @params: field [string] -> destination group such as tier or status
* @params: cell [string] -> raw cell
* @returns: value [string] -> label value or the original cell
*/
func labeled(labels []LabelMap, field, cell string) string {
	if value, ok := applyLabel(labels, field, cell); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return cell
}

/*
applyLabel finds the first label whose field and source match a cell.

* @brief: applyLabel -> looks up one canonical label
* @params: labels [[]LabelMap] -> plan labels
* @params: field [string] -> field group
* @params: cell [string] -> raw cell
* @returns: value [string] -> canonical value
* @returns: ok [bool] -> true when a label matches
*/
func applyLabel(labels []LabelMap, field, cell string) (string, bool) {
	want := fieldGroup(field)
	for _, label := range labels {
		if fieldGroup(label.Field) != want {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(label.Source), strings.TrimSpace(cell)) {
			return label.Value, true
		}
	}
	return "", false
}

/*
fieldGroup reduces a destination or label field to a short group name.

* @brief: fieldGroup -> normalizes a label field name
* @params: field [string] -> destination or label field
* @returns: group [string] -> short field group
*/
func fieldGroup(field string) string {
	f := strings.ToLower(strings.TrimSpace(field))
	f = strings.TrimPrefix(f, "enrollment.")
	f = strings.TrimPrefix(f, "employee.")
	f = strings.TrimPrefix(f, "dependent.")
	switch f {
	case "coverage_tier", "tier":
		return "tier"
	case "status", "enrollment_status":
		return "status"
	case "relationship", "rel":
		return "relationship"
	case "employment_status":
		return "employment"
	case "type", "enrollment_type":
		return "type"
	default:
		return f
	}
}

/*
interpretTierCell reads a wide benefit cell as a tier, a waiver, or both.

* @brief: interpretTierCell -> reads a coverage cell that may be a tier or a waiver
* @params: cell [string] -> raw coverage cell
* @params: labels [[]LabelMap] -> plan labels
* @returns: tier [string] -> canonical tier, or empty
* @returns: status [string] -> canonical status, or empty
* @returns: ok [bool] -> true when the cell is a known tier or status
*/
func interpretTierCell(cell string, labels []LabelMap) (string, string, bool) {
	tier := ""
	status := ""
	if value, ok := applyLabel(labels, "tier", cell); ok {
		if canonical, okTier := canonTier(value); okTier {
			tier = canonical
		}
	}
	if value, ok := applyLabel(labels, "status", cell); ok {
		if canonical, okStatus := canonStatusValue(value); okStatus {
			status = canonical
		}
	}
	if status == "" && isWaiverText(cell) {
		status = "WAIVED"
	}
	if tier == "" {
		if canonical, okTier := canonTier(cell); okTier {
			tier = canonical
		}
	}
	if tier == "" && status == "" {
		return "", "", false
	}
	return tier, status, true
}

/*
parseEmployment maps an employment cell to active, inactive, or unknown.

* @brief: parseEmployment -> reads an employment status cell
* @params: cell [string] -> raw status
* @params: labels [[]LabelMap] -> plan labels
* @returns: status [*bool] -> true for active and false for inactive
* @returns: ok [bool] -> true when the cell is a known employment status
*/
func parseEmployment(cell string, labels []LabelMap) (*bool, bool) {
	value := labeled(labels, "employment", cell)
	switch normKey(value) {
	case "true", "t", "yes", "y", "1", "active", "employed", "newhire":
		status := true
		return &status, true
	case "false", "f", "no", "n", "0", "inactive", "terminated":
		status := false
		return &status, true
	default:
		return nil, false
	}
}

/*
parseEmploymentNote accepts completed employment-end statements and leaves other narrative claims for review.

* @brief: parseEmploymentNote -> reads explicit past employment-end clauses without interpreting coverage dates
* @params: note [string] -> original note from an employee row
* @params: dateFormat [string] -> date convention declared by the document
* @returns: status [*bool] -> inactive for an unqualified completed employment-end statement, otherwise nil
* @returns: relevant [bool] -> true when the note discusses employment and needs interpretation
*/
func parseEmploymentNote(note, dateFormat string) (*bool, bool) {
	text := strings.ToLower(strings.TrimSpace(note))
	if !strings.Contains(text, "employment") && !strings.Contains(text, "employed") {
		return nil, false
	}
	ended := false
	text = strings.ReplaceAll(text, ". ", ";")
	for _, clause := range strings.FieldsFunc(text, func(r rune) bool { return r == ';' || r == '\n' }) {
		clause = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(clause), "."))
		employment := strings.Contains(clause, "employment") || strings.Contains(clause, "employed")
		if !employment && (strings.Contains(clause, "coverage") || strings.Contains(clause, "covered") || strings.Contains(clause, "cobra") || strings.Contains(clause, "medical") || strings.Contains(clause, "dental") || strings.Contains(clause, "vision")) {
			continue
		}
		for _, word := range strings.FieldsFunc(clause, func(r rune) bool { return !unicode.IsLetter(r) }) {
			switch word {
			case "not", "no", "never", "unknown", "uncertain", "unconfirmed", "if", "may", "might", "will", "would", "could", "planned", "proposed", "expected", "reported", "disputed", "retracted", "rehired", "resumed", "active", "still", "currently", "pending":
				return nil, true
			}
		}
		if !employment {
			continue
		}
		matched := false
		for _, prefix := range []string{"employment ended", "employment terminated"} {
			if clause != prefix && !strings.HasPrefix(clause, prefix+" ") && !strings.HasPrefix(clause, prefix+":") {
				continue
			}
			tail := strings.TrimSpace(strings.TrimPrefix(clause, prefix))
			tail = strings.TrimSpace(strings.TrimPrefix(tail, ":"))
			tail = strings.TrimPrefix(tail, "on ")
			if tail != "" {
				_, present, valid := parseDate(tail, dateFormat)
				if !present || !valid {
					valid = false
					for _, layout := range []string{"January 2, 2006", "January 2 2006", "2 January 2006", "Jan 2, 2006", "Jan 2 2006", "2 Jan 2006"} {
						if _, err := time.Parse(layout, tail); err == nil {
							valid = true
							break
						}
					}
				}
				if !valid {
					return nil, true
				}
			}
			matched = true
			ended = true
		}
		if !matched {
			return nil, true
		}
	}
	if ended {
		status := false
		return &status, true
	}
	return nil, true
}

/*
assignString stores the first non-empty value in a string pointer.

* @brief: assignString -> stores a value when the destination is empty
* @params: dst [**string] -> field to set
* @params: value [string] -> trimmed cell or canonical value
*/
func assignString(dst **string, value string) {
	if dst == nil || *dst != nil || strings.TrimSpace(value) == "" {
		return
	}
	*dst = strPtr(strings.TrimSpace(value))
}

/*
setString fills an empty string or records a conflict when both values differ.

* @brief: setString -> fills a nil string without overwriting a conflict
* @params: dst [**string] -> stored field
* @params: src [*string] -> incoming field
* @params: equal [func(string, string) bool] -> comparison for the two texts
* @params: onConflict [func()] -> called when both values are set and differ
*/
func setString(dst **string, src *string, equal func(string, string) bool, onConflict func()) {
	if src == nil || dst == nil {
		return
	}
	if *dst == nil {
		*dst = CloneString(src)
		return
	}
	if equal(**dst, *src) {
		return
	}
	onConflict()
}

/*
setBool fills an empty bool or records a conflict when both values differ.

* @brief: setBool -> fills a nil bool without overwriting a conflict
* @params: dst [**bool] -> stored field
* @params: src [*bool] -> incoming field
* @params: onConflict [func()] -> called when both values are set and differ
*/
func setBool(dst **bool, src *bool, onConflict func()) {
	if src == nil || dst == nil {
		return
	}
	if *dst == nil {
		*dst = cloneBool(src)
		return
	}
	if **dst == *src {
		return
	}
	onConflict()
}

/*
sameSSNText compares Social Security numbers by their digits.

* @brief: sameSSNText -> reports whether two SSN texts are the same number
* @params: a [string] -> first SSN
* @params: b [string] -> second SSN
* @returns: same [bool] -> true when the digits match
*/
func sameSSNText(a, b string) bool {
	da, db := digitsOnly(a), digitsOnly(b)
	if da != "" && da == db {
		return true
	}
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

/*
sameStringPtr compares two optional strings.

* @brief: sameStringPtr -> reports whether two optional strings are equal
* @params: a [*string] -> first value
* @params: b [*string] -> second value
* @returns: same [bool] -> true when both are nil or both hold the same text
*/
func sameStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

/*
personKeys returns folded name keys for "first last" and "last first".

* @brief: personKeys -> builds match keys for a first and last name
* @params: first [string] -> given name
* @params: last [string] -> family name
* @returns: keys [[]string] -> folded keys with punctuation and accents removed
*/
func personKeys(first, last string) []string {
	forward := foldText(strings.TrimSpace(first + " " + last))
	backward := foldText(strings.TrimSpace(last + " " + first))
	return uniqueStrings([]string{forward, backward})
}

/*
rawNameKeys splits a display name and returns its match keys.

* @brief: rawNameKeys -> builds match keys for a full name
* @params: raw [string] -> display name
* @returns: keys [[]string] -> folded keys
*/
func rawNameKeys(raw string) []string {
	first, last := SplitName(raw)
	return personKeys(first, last)
}

/*
foldPerson returns the folded first-and-last key.

* @brief: foldPerson -> folds a first and last name into one key
* @params: first [string] -> given name
* @params: last [string] -> family name
* @returns: key [string] -> folded name, empty when both parts are empty
*/
func foldPerson(first, last string) string {
	keys := personKeys(first, last)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

/*
foldText lowercases text, strips accents, and drops punctuation.

* @brief: foldText -> normalizes a name for comparison
* @params: raw [string] -> name text
* @returns: key [string] -> letters and digits only
*/
func foldText(raw string) string {
	raw = accentFold.Replace(raw)
	var b strings.Builder
	for _, r := range raw {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

/*
keysOverlap reports whether two key lists share a non-empty key.

* @brief: keysOverlap -> reports whether two name key lists intersect
* @params: a [[]string] -> first keys
* @params: b [[]string] -> second keys
* @returns: ok [bool] -> true when one key appears in both lists
*/
func keysOverlap(a, b []string) bool {
	for _, left := range a {
		if left == "" {
			continue
		}
		for _, right := range b {
			if left == right {
				return true
			}
		}
	}
	return false
}

/*
samePersonText reports whether two display names fold to the same person.

* @brief: samePersonText -> compares two display names
* @params: a [string] -> first name
* @params: b [string] -> second name
* @returns: same [bool] -> true when the folded names match
*/
func samePersonText(a, b string) bool {
	return keysOverlap(rawNameKeys(a), rawNameKeys(b))
}

/*
employeesNamed returns employees whose normalized name matches a display name.

* @brief: employeesNamed -> finds employees by normalized name
* @params: employees [[]Employee] -> employees to search
* @params: raw [string] -> display name
* @returns: hits [[]Employee] -> matching employees
*/
func employeesNamed(employees []Employee, raw string) []Employee {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	want := rawNameKeys(raw)
	var hits []Employee
	for _, employee := range employees {
		if keysOverlap(personKeys(employee.FirstName, employee.LastName), want) {
			hits = append(hits, employee)
		}
	}
	return hits
}

/*
rowPrefix identifies a data row in an issue detail.

* @brief: rowPrefix -> identifies a data row in an issue detail
* @params: rowNumber [int] -> one-based data row number
* @params: person [*Proposed] -> person parsed from the row, or nil
* @returns: prefix [string] -> row label
*/
func rowPrefix(rowNumber int, person *Proposed) string {
	if person == nil {
		return "row " + strconv.Itoa(rowNumber)
	}
	name := DisplayName(person.FirstName, person.LastName)
	if name == "" {
		return "row " + strconv.Itoa(rowNumber)
	}
	return "row " + strconv.Itoa(rowNumber) + " " + name
}

/*
DisplayName joins a first and last name.

* @brief: DisplayName -> joins a first and last name
* @params: first [string] -> given name
* @params: last [string] -> family name
* @returns: name [string] -> trimmed display name
*/
func DisplayName(first, last string) string {
	return strings.TrimSpace(first + " " + last)
}

/*
strPtr returns a pointer to a copy of a string.

* @brief: strPtr -> returns a pointer to a string copy
* @params: value [string] -> text to store
* @returns: out [*string] -> pointer to a copy
*/
func strPtr(value string) *string {
	return &value
}

/*
StringValue returns a pointer's text or missing.

* @brief: StringValue -> formats an optional string for an issue
* @params: value [*string] -> optional text
* @returns: text [string] -> stored text or missing
*/
func StringValue(value *string) string {
	if value == nil {
		return "missing"
	}
	return *value
}

/*
boolText formats an optional bool.

* @brief: boolText -> formats an optional bool for an issue
* @params: value [*bool] -> optional bool
* @returns: text [string] -> true, false, or missing
*/
func boolText(value *bool) string {
	if value == nil {
		return "missing"
	}
	if *value {
		return "true"
	}
	return "false"
}

/*
quote wraps a cell in quotes for an issue detail.

* @brief: quote -> wraps text in double quotes
* @params: value [string] -> cell text
* @returns: quoted [string] -> quoted text
*/
func quote(value string) string {
	return `"` + value + `"`
}

/*
firstString returns the first non-nil string pointer.

* @brief: firstString -> chooses the first present optional string
* @params: values [...*string] -> candidates in preference order
* @returns: value [*string] -> first non-nil value, cloned
*/
func firstString(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return CloneString(value)
		}
	}
	return nil
}

/*
uniqueStrings returns non-empty strings once, preserving order.

* @brief: uniqueStrings -> drops empty and duplicate strings
* @params: values [[]string] -> input strings
* @returns: out [[]string] -> unique non-empty strings
*/
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

var tierByKey = map[string]string{
	"employee":           "EMPLOYEE",
	"e":                  "EMPLOYEE",
	"ee":                 "EMPLOYEE",
	"employeeonly":       "EMPLOYEE",
	"emponly":            "EMPLOYEE",
	"employeespouseonly": "EMPLOYEE_SPOUSE_ONLY",
	"employeespouse":     "EMPLOYEE_SPOUSE_ONLY",
	"eespouse":           "EMPLOYEE_SPOUSE_ONLY",
	"eesp":               "EMPLOYEE_SPOUSE_ONLY",
	"es":                 "EMPLOYEE_SPOUSE_ONLY",
	"employeechildren":   "EMPLOYEE_CHILDREN",
	"employeechild":      "EMPLOYEE_CHILDREN",
	"eechildren":         "EMPLOYEE_CHILDREN",
	"eechild":            "EMPLOYEE_CHILDREN",
	"eech":               "EMPLOYEE_CHILDREN",
	"ec":                 "EMPLOYEE_CHILDREN",
	"employeefamily":     "EMPLOYEE_FAMILY",
	"eefamily":           "EMPLOYEE_FAMILY",
	"family":             "EMPLOYEE_FAMILY",
	"fam":                "EMPLOYEE_FAMILY",
	"ef":                 "EMPLOYEE_FAMILY",
}

var accentFold = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a",
	"è", "e", "é", "e", "ê", "e", "ë", "e",
	"ì", "i", "í", "i", "î", "i", "ï", "i",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o",
	"ù", "u", "ú", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c", "ý", "y", "ÿ", "y",
	"À", "A", "Á", "A", "Â", "A", "Ã", "A", "Ä", "A", "Å", "A",
	"È", "E", "É", "E", "Ê", "E", "Ë", "E",
	"Ì", "I", "Í", "I", "Î", "I", "Ï", "I",
	"Ò", "O", "Ó", "O", "Ô", "O", "Õ", "O", "Ö", "O",
	"Ù", "U", "Ú", "U", "Û", "U", "Ü", "U",
	"Ñ", "N", "Ç", "C", "Ý", "Y",
)
