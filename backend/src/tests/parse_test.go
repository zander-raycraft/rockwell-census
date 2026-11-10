package tests

import (
	"census/src/parse"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

/*
TestParseRetainsFixtureRows checks all supplied table boundaries after footer handling changes.

* @brief: TestParseRetainsFixtureRows -> preserves header and data counts across the 33 fixtures
* @params: t [*testing.T] -> test runner
*/
func TestParseRetainsFixtureRows(t *testing.T) {
	type fixture struct {
		File   string `json:"file"`
		Header int    `json:"headerRecord"`
		Rows   int    `json:"detailRecordCount"`
	}
	var manifest struct {
		Files []fixture `json:"files"`
	}
	body, err := os.ReadFile("../../../examples/synthetic/batch_02/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Files {
		manifest.Files[i].File = "synthetic/batch_02/" + manifest.Files[i].File
	}
	fixtures := append(manifest.Files, []fixture{
		{"company_a_census.csv", 3, 13}, {"company_b_payroll.csv", 1, 9}, {"company_b_enrollment.csv", 1, 23},
		{"synthetic/company_c_census.csv", 4, 12}, {"synthetic/company_d_payroll.csv", 1, 11}, {"synthetic/company_d_enrollment.csv", 4, 22},
		{"synthetic/company_e_staff.csv", 3, 8}, {"synthetic/company_e_benefit_elections.csv", 1, 9}, {"synthetic/company_e_household.csv", 3, 11},
	}...)
	for _, fixture := range fixtures {
		t.Run(fixture.File, func(t *testing.T) {
			body, err := os.ReadFile("../../../examples/" + fixture.File)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parse.Parse(parse.Document{Filename: fixture.File, Body: body})
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Kind != "csv" || parsed.HeaderRow != fixture.Header || len(parsed.Rows) != fixture.Rows {
				t.Errorf("Parse(%s) = kind %q header %d rows %d; want csv header %d rows %d", fixture.File, parsed.Kind, parsed.HeaderRow, len(parsed.Rows), fixture.Header, fixture.Rows)
			}
		})
	}
}

/*
TestParseDoesNotDiscardNamesContainingSummaryWords preserves real person rows while skipping numeric summaries.

* @brief: TestParseDoesNotDiscardNamesContainingSummaryWords -> distinguishes names from report markers
* @params: t [*testing.T] -> test runner
*/
func TestParseDoesNotDiscardNamesContainingSummaryWords(t *testing.T) {
	parsed, err := parse.Parse(parse.Document{Filename: "census.csv", Body: []byte("Name,DOB,SSN\nTotal Smith,1980-01-01,901111111\nHeadcount Jones,1981-01-01,902222222\nREPORT TOTAL,2,Unverified\n")})
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Rows) != 2 {
		t.Errorf("Parse(summary words) rows = %#v; want both people and no REPORT TOTAL row", parsed.Rows)
	}
}

/*
TestTablePreviewIncludesLateCategoricalEvidence exposes labels outside sampled rows without full-file prompts.

* @brief: TestTablePreviewIncludesLateCategoricalEvidence -> includes full-file counts and rare values
* @params: t [*testing.T] -> test runner
*/
func TestTablePreviewIncludesLateCategoricalEvidence(t *testing.T) {
	parsed := parse.Parsed{Headers: []string{"Name", "Medical Status"}}
	for i := 0; i < 100; i++ {
		parsed.Rows = append(parsed.Rows, []string{"Person " + strconv.Itoa(i), "Active"})
	}
	parsed.Rows[82][1] = "Unrecognized late status"
	parsed.Rows[84][1] = ""
	preview := parse.TablePreview(parsed)
	for _, want := range []string{`"Unrecognized late status" count=1`, `"Active" count=98`, "blank=1", "row 100:", "data_rows_not_sampled: 80"} {
		if !strings.Contains(preview, want) {
			t.Errorf("tablePreview(late labels) missing %q", want)
		}
	}
	if strings.Contains(preview, "row 83:") {
		t.Error("test requires rare row outside selected samples")
	}
	parsed.Rows[0][0] = strings.Repeat("x", 40000)
	for i := 0; i < 100; i++ {
		parsed.Headers = append(parsed.Headers, strings.Repeat("h", 400))
	}
	preview = parse.TablePreview(parsed)
	if utf8.RuneCountInString(preview) > 32000 || !strings.Contains(preview, "preview clipped") {
		t.Errorf("tablePreview(oversize) length=%d; want at most32000 and explicit clipping notice", utf8.RuneCountInString(preview))
	}
}
