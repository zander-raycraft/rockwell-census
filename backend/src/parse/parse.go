package parse

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

/*
Document is the source file for one census write. An empty body skips the documents insert.

* @brief: Document -> filename, content type, and original bytes
* @returns: document [Document] -> the uploaded file stored with a census save
*/
type Document struct {
	SourceSystem string
	Filename     string
	ContentType  string
	Body         []byte
}

/*
Parsed is the delimited table or email preview produced from one document.

* @brief: Parsed -> rows and a bounded preview for one document
* @returns: value [Parsed] -> kind, delimiter, header, data rows, preview, and skipped notes
*/
type Parsed struct {
	Kind      string
	Delimiter string
	HeaderRow int
	Headers   []string
	Rows      [][]string
	Preview   string
	Skipped   []string
}

/*
Parse classifies one document as a delimited table or an email.

* @brief: Parse -> splits a census file into rows or keeps an email body as a preview
* @params: document [Document] -> file name, content type, and original bytes
* @returns: parsed [Parsed] -> header and every data row, or an email preview
* @returns: err [error] -> non-nil when the document cannot be classified
*/
func Parse(document Document) (Parsed, error) {
	body := append([]byte(nil), document.Body...)
	if bytes.HasPrefix(body, []byte{0xEF, 0xBB, 0xBF}) {
		body = body[3:]
	}
	if isTextPlain(document.ContentType) {
		return emailParsed(body), nil
	}
	delim, records, ok := detectTable(body)
	if !ok {
		return emailParsed(body), nil
	}
	return tableParsed(delim, records), nil
}

/*
isTextPlain reports whether the content type is text/plain, ignoring parameters.

* @brief: isTextPlain -> reports whether a document is an email body
* @params: contentType [string] -> MIME type from the upload
* @returns: plain [bool] -> true when the type is text/plain
*/
func isTextPlain(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == "text/plain"
}

/*
emailParsed builds an email result whose preview is at most 8000 characters.

* @brief: emailParsed -> stores a trimmed email body and no data rows
* @params: body []byte -> document bytes after a BOM strip
* @returns: parsed [Parsed] -> kind email and a bounded preview
*/
func emailParsed(body []byte) Parsed {
	text := strings.TrimSpace(string(body))
	text = clipRunes(text, 8000)
	return Parsed{
		Kind:      "email",
		Delimiter: "",
		HeaderRow: 0,
		Headers:   []string{},
		Rows:      [][]string{},
		Preview:   strings.Clone(text),
		Skipped:   []string{},
	}
}

/*
detectTable selects the delimiter whose records share the most consistent width.

* @brief: detectTable -> chooses comma, semicolon, pipe, or tab
* @params: body []byte -> document bytes after a BOM strip
* @returns: delim [string] -> winning delimiter, or empty when the body is not a table
* @returns: records [[][]string] -> logical records read with that delimiter
* @returns: ok [bool] -> true when a header and detail rows were found
*/
func detectTable(body []byte) (string, [][]string, bool) {
	candidates := []string{",", ";", "|", "\t"}
	bestScore := 0
	bestWidth := 0
	bestRatio := 0.0
	bestDelim := ""
	var bestRecords [][]string
	for _, delim := range candidates {
		records, err := readRecords(body, delim)
		if err != nil {
			continue
		}
		width, count, nonempty := widthMode(records)
		if width < 2 || count < 2 || nonempty < 2 {
			continue
		}
		ratio := float64(count) / float64(nonempty)
		if ratio < 0.6 {
			continue
		}
		if _, ok := chooseHeader(records, width); !ok {
			continue
		}
		if count > bestScore || (count == bestScore && width > bestWidth) ||
			(count == bestScore && width == bestWidth && ratio > bestRatio) {
			bestScore = count
			bestWidth = width
			bestRatio = ratio
			bestDelim = delim
			bestRecords = records
		}
	}
	if bestDelim == "" {
		return "", nil, false
	}
	return bestDelim, bestRecords, true
}

/*
readRecords parses the body with encoding/csv so quotes and embedded newlines stay intact.

* @brief: readRecords -> reads logical records for one delimiter
* @params: body []byte -> document bytes
* @params: delim [string] -> one-character delimiter
* @returns: records [[][]string] -> every logical record
* @returns: err [error] -> non-nil when the delimiter cannot parse the body
*/
func readRecords(body []byte, delim string) ([][]string, error) {
	reader := csv.NewReader(bytes.NewReader(body))
	reader.Comma = []rune(delim)[0]
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	reader.ReuseRecord = false
	return reader.ReadAll()
}

/*
widthMode returns the most common field count among non-blank records.

* @brief: widthMode -> finds the field count shared by detail records
* @params: records [[][]string] -> logical records
* @returns: width [int] -> most common field count
* @returns: count [int] -> how many non-blank records have that count
* @returns: nonempty [int] -> how many records contain a value
*/
func widthMode(records [][]string) (int, int, int) {
	counts := map[int]int{}
	nonempty := 0
	for _, rec := range records {
		if blankRecord(rec) {
			continue
		}
		nonempty++
		counts[len(rec)]++
	}
	width, count := 0, 0
	for w, c := range counts {
		if c > count || (c == count && w > width) {
			width, count = w, c
		}
	}
	return width, count, nonempty
}

/*
chooseHeader returns the first non-empty record that looks like the column header.

* @brief: chooseHeader -> finds the header record index
* @params: records [[][]string] -> logical records
* @params: width [int] -> field count of the detail records
* @returns: index [int] -> zero-based record index
* @returns: ok [bool] -> true when a header with following detail exists
*/
func chooseHeader(records [][]string, width int) (int, bool) {
	for i, rec := range records {
		if !looksLikeHeader(rec, width) {
			continue
		}
		if !hasDetailAfter(records, i, width) {
			continue
		}
		return i, true
	}
	return 0, false
}

/*
looksLikeHeader reports whether a record is wide enough and mostly populated.

* @brief: looksLikeHeader -> distinguishes a header from a title or footer
* @params: rec [[]string] -> one logical record
* @params: width [int] -> expected field count
* @returns: ok [bool] -> true when this record can be the header
*/
func looksLikeHeader(rec []string, width int) bool {
	if len(rec) != width || blankRecord(rec) || footerRecord(rec) {
		return false
	}
	need := width / 2
	if need < 2 {
		need = 2
	}
	return populatedCount(rec) >= need
}

/*
hasDetailAfter reports whether a later record shares the detail width.

* @brief: hasDetailAfter -> requires detail records after a header candidate
* @params: records [[][]string] -> logical records
* @params: index [int] -> header candidate index
* @params: width [int] -> expected field count
* @returns: ok [bool] -> true when a later non-blank, non-footer record has that width
*/
func hasDetailAfter(records [][]string, index, width int) bool {
	for _, rec := range records[index+1:] {
		if len(rec) != width || blankRecord(rec) || footerRecord(rec) {
			continue
		}
		return true
	}
	return false
}

/*
tableParsed splits records into a header, data rows, and skipped notes.

* @brief: tableParsed -> builds the parsed table and its bounded preview
* @params: delim [string] -> detected delimiter
* @params: records [[][]string] -> logical records
* @returns: parsed [Parsed] -> kind csv, every data row, and a preview of at most 20 distributed rows
*/
func tableParsed(delim string, records [][]string) Parsed {
	width, _, _ := widthMode(records)
	headerIdx, _ := chooseHeader(records, width)
	headers := append([]string(nil), records[headerIdx]...)
	parsed := Parsed{
		Kind:      "csv",
		Delimiter: delim,
		HeaderRow: headerIdx + 1,
		Headers:   headers,
		Rows:      [][]string{},
		Skipped:   []string{},
	}
	for i, rec := range records {
		if i == headerIdx {
			continue
		}
		switch {
		case blankRecord(rec):
			parsed.Skipped = append(parsed.Skipped, skipNote(i, "blank", rec))
		case i < headerIdx:
			parsed.Skipped = append(parsed.Skipped, skipNote(i, "title", rec))
		case footerRecord(rec):
			parsed.Skipped = append(parsed.Skipped, skipNote(i, "footer", rec))
		case sameRecord(rec, headers):
			parsed.Skipped = append(parsed.Skipped, skipNote(i, "repeated header", rec))
		default:
			parsed.Rows = append(parsed.Rows, append([]string(nil), rec...))
		}
	}
	parsed.Preview = tablePreview(parsed)
	return parsed
}

/*
skipNote formats one skipped record for the preview.

* @brief: skipNote -> describes a title, blank, repeated header, or footer
* @params: index [int] -> zero-based logical record index
* @params: kind [string] -> why the record was skipped
* @params: rec [[]string] -> the skipped cells
* @returns: note [string] -> one clipped note line
*/
func skipNote(index int, kind string, rec []string) string {
	if kind == "blank" {
		return "record " + strconv.Itoa(index+1) + " blank"
	}
	return "record " + strconv.Itoa(index+1) + " " + kind + ": " + clipRunes(strings.Join(rec, " | "), 240)
}

/*
tablePreview builds the bounded text sent to the model.

* @brief: tablePreview -> formats the delimiter, header, skipped notes, and at most 20 rows
* @params: parsed [Parsed] -> classified table
* @returns: preview [string] -> text that omits unsampled data rows and caps evidence and sampled rows
*/
func tablePreview(parsed Parsed) string {
	var b strings.Builder
	b.WriteString("delimiter: " + delimiterLabel(parsed.Delimiter) + "\n")
	b.WriteString("header_row: " + strconv.Itoa(parsed.HeaderRow) + "\n")
	b.WriteString("headers: " + previewLine(parsed.Headers) + "\n")
	b.WriteString("skipped:\n")
	limit := len(parsed.Skipped)
	if limit > 40 {
		limit = 40
	}
	if limit == 0 {
		b.WriteString("(none)\n")
	}
	for i := 0; i < limit; i++ {
		b.WriteString(parsed.Skipped[i] + "\n")
	}
	if len(parsed.Skipped) > limit {
		b.WriteString("skipped_not_shown: " + strconv.Itoa(len(parsed.Skipped)-limit) + "\n")
	}
	shown := len(parsed.Rows)
	if shown > 20 {
		shown = 20
	}
	b.WriteString("column_evidence (counts use all data rows; values are bounded, not definitions):\n")
	b.WriteString(columnEvidence(parsed))
	b.WriteString("data_row_count: " + strconv.Itoa(len(parsed.Rows)) + "\n")
	b.WriteString("data_rows_not_sampled: " + strconv.Itoa(len(parsed.Rows)-shown) + "\n")
	for i := 0; i < shown; i++ {
		index := i
		if len(parsed.Rows) > shown {
			index = i * (len(parsed.Rows) - 1) / (shown - 1)
		}
		b.WriteString("row " + strconv.Itoa(index+1) + ": " + previewLine(parsed.Rows[index]) + "\n")
	}
	preview := b.String()
	if utf8.RuneCountInString(preview) > 32000 {
		const notice = "\n(preview clipped; selected sample rows may be omitted)\n"
		preview = clipRunes(preview, 32000-len(notice)) + notice
	}
	return strings.Clone(preview)
}

/*
previewLine joins cells for the model preview and clips each cell.

* @brief: previewLine -> renders one record without embedding raw newlines
* @params: cells [[]string] -> header or data cells
* @returns: line [string] -> cells joined for the preview
*/
func previewLine(cells []string) string {
	parts := make([]string, len(cells))
	for i, cell := range cells {
		cell = strings.ReplaceAll(cell, "\r\n", " ")
		cell = strings.ReplaceAll(cell, "\n", " ")
		cell = strings.ReplaceAll(cell, "\r", " ")
		parts[i] = clipRunes(cell, 400)
	}
	return strings.Join(parts, " | ")
}

/*
delimiterLabel names a delimiter for the preview.

* @brief: delimiterLabel -> returns a readable delimiter name
* @params: delim [string] -> ",", ";", "|", or a tab
* @returns: label [string] -> name and character of the delimiter
*/
func delimiterLabel(delim string) string {
	switch delim {
	case ",":
		return "comma (,)"
	case ";":
		return "semicolon (;)"
	case "|":
		return "pipe (|)"
	case "\t":
		return "tab (\\t)"
	default:
		return delim
	}
}

/*
blankRecord reports whether every cell is empty or whitespace.

* @brief: blankRecord -> reports whether a record has no values
* @params: rec [[]string] -> one logical record
* @returns: blank [bool] -> true when every cell is blank
*/
func blankRecord(rec []string) bool {
	for _, cell := range rec {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

/*
populatedCount counts cells that contain a non-whitespace value.

* @brief: populatedCount -> counts filled cells
* @params: rec [[]string] -> one logical record
* @returns: n [int] -> number of cells with a value
*/
func populatedCount(rec []string) int {
	n := 0
	for _, cell := range rec {
		if strings.TrimSpace(cell) != "" {
			n++
		}
	}
	return n
}

/*
footerRecord reports whether the first cell marks a report total or headcount.

* @brief: footerRecord -> detects total and headcount rows
* @params: rec [[]string] -> one logical record
* @returns: footer [bool] -> true when the first cell contains TOTAL, HEADCOUNT, or REPORT TOTAL
*/
func footerRecord(rec []string) bool {
	if len(rec) == 0 {
		return false
	}
	cell := strings.ToUpper(strings.TrimSpace(rec[0]))
	label, _, _ := strings.Cut(cell, ":")
	switch strings.TrimSpace(label) {
	case "REPORT TOTAL", "TOTAL", "HEADCOUNT", "TOTAL EMPLOYEES", "TOTAL DEPENDENTS", "TOTAL MEMBERS", "TOTAL ROWS", "TOTAL RECORDS":
		return true
	}
	return false
}

/*
sameRecord reports whether two records match after trimming, ignoring case.

* @brief: sameRecord -> detects a repeated header
* @params: a [[]string] -> candidate record
* @params: b [[]string] -> header cells
* @returns: same [bool] -> true when every cell matches
*/
func sameRecord(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(strings.TrimSpace(a[i]), strings.TrimSpace(b[i])) {
			return false
		}
	}
	return true
}

/*
clipRunes returns at most n Unicode characters.

* @brief: clipRunes -> limits text to a character count
* @params: s [string] -> input text
* @params: n [int] -> maximum number of characters
* @returns: clipped [string] -> the leading characters
*/
func clipRunes(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}

/*
columnEvidence summarizes complete-table categorical values without sending the full census.

* @brief: columnEvidence -> exposes late and rare labels absent from sampled rows
* @params: parsed [Parsed] -> headers and every data row
* @returns: evidence [string] -> bounded counts and examples, including omitted-value notices
*/
func columnEvidence(parsed Parsed) string {
	var b strings.Builder
	for col, header := range parsed.Headers {
		key := strings.ToLower(header)
		categorical := false
		for _, term := range []string{"relationship", "rel", "status", "tier", "coverage", "election", "benefit", "medical", "dental", "vision", "employment"} {
			if strings.Contains(key, term) {
				categorical = true
				break
			}
		}
		if !categorical {
			continue
		}
		counts := map[string]int{}
		blank, overflow := 0, 0
		for _, row := range parsed.Rows {
			if col >= len(row) || strings.TrimSpace(row[col]) == "" {
				blank++
				continue
			}
			value := strings.TrimSpace(row[col])
			if _, exists := counts[value]; !exists && len(counts) >= 16 {
				overflow++
				continue
			}
			counts[value]++
		}
		fmt.Fprintf(&b, "%q: blank=%d", clipRunes(header, 120), blank)
		values := make([]string, 0, len(counts))
		for value := range counts {
			values = append(values, value)
		}
		sort.Strings(values)
		for _, value := range values {
			fmt.Fprintf(&b, " | %q count=%d", clipRunes(value, 120), counts[value])
		}
		if overflow > 0 {
			fmt.Fprintf(&b, " | other_value_rows=%d (not shown)", overflow)
		}
		b.WriteString("\n")
		if b.Len() >= 12000 {
			return clipRunes(b.String(), 12000) + "\n(further column evidence omitted)\n"
		}
	}
	return b.String()
}

/*
ClipRunes returns at most n Unicode characters.

* @brief: ClipRunes -> limits text to a character count
* @params: s [string] -> input text
* @params: n [int] -> maximum number of characters
* @returns: clipped [string] -> the leading characters
*/
func ClipRunes(s string, n int) string {
	return clipRunes(s, n)
}

/*
TablePreview returns the bounded text sent to the extractor.

* @brief: TablePreview -> formats headers and sample rows for the model
* @params: parsed [Parsed] -> parsed table or email
* @returns: preview [string] -> bounded preview text
*/
func TablePreview(parsed Parsed) string {
	return tablePreview(parsed)
}
