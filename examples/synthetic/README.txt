SYNTHETIC CENSUS TEST FIXTURES

All companies, people, plans, addresses, and identifiers are fictional.
SSNs use deliberately synthetic 9xx prefixes, following the supplied examples.
These are test identifiers, not production-valid SSNs. A production SSN validator
may flag all of them; that is separate from missing values and source conflicts.
Snapshot date: 2026-10-31. Future elections are explicitly dated 2027.
Original example files were left unchanged.

FILE FORMATS
company_c_census.csv: comma; UTF-8; header is CSV record 4; 11 columns.
company_d_payroll.csv: comma; UTF-8; header is CSV record 1; 15 columns.
company_d_enrollment.csv: semicolon; UTF-8 with BOM; header record 4; 13 columns.
company_e_staff.csv: pipe; UTF-8; header record 3; 9 columns; DD/MM/YYYY dates.
company_e_benefit_elections.csv: comma; UTF-8; header record 1; 15 columns; ISO dates.
company_e_household.csv: semicolon; UTF-8; header record 3; 12 columns; DD/MM/YYYY.

All six files are syntactically valid delimited text with consistent field counts
within each file. Layout and data irregularities are intentional. CSV record
numbers are logical records: an embedded newline can span physical file lines.

COMPANY C: HAND-MAINTAINED CENSUS
7 employee rows and 5 dependent rows. The footer incorrectly reports 8 employees.
Ignore title, metadata, blank records, repeated header, and footer as people.
Normalize last-name-first names, relationship aliases, dates, and election labels.
Rebecca Hale: spouse Luis has medical, not dental; child Noah explicitly not enrolled.
Owen Price: child Iris has medical/dental. Blank child vision is unknown.
Mei Chen: missing DOB; medical/dental explicitly waived; vision unknown.
Luca D'Angelo: family elections; spouse Simone's individual coverage unspecified.
Eli has no relationship code; note identifies Luca's child, not confirmed coverage.
Avery Morgan: new hire with pending medical; blank benefits remain unknown.
Ellis Brown: COBRA note; do not equate ended employment with ended benefit coverage.
Jordan Reed: invalid DOB 02/30/1988 requires review, not a guessed correction.
Eli's note includes a quoted embedded newline. Addresses include quoted commas.

COMPANY D: PAYROLL + LONG-FORM ENROLLMENT
Payroll: 11 detail rows, 10 unique Worker Numbers; 00706 is duplicated exactly.
Enrollment: 21 detail rows; 20 after removing one exact duplicate (Nico medical).
There are 11 subscriber references, including one intentionally ambiguous record.
Expected confident/candidate links (never join the unrelated source ID strings):
  00701 <-> BEN-41: Elizabeth / Liz Bennett; same DOB and SSN.
  00702 <-> BEN-42: Alexander / Alex Patel; DOB and SSN identify the person.
  00703 <-> BEN-43: Alexandra / Alex Patel; different DOB and SSN from BEN-42.
  00704 <-> BEN-44: Sean O'Neill / Sean ONeill; punctuation variation.
  00705 <-> BEN-45: Isabel Ruiz-Martinez / Isabel Ruiz; same DOB and SSN.
  00706 <-> BEN-46: Marcus Brooks; employment LOA and active benefits coexist.
  00707 <-> BEN-47: Naomi Kim; enrollment supplies DOB missing from payroll.
  00708 <-> BEN-48: Benjamin / Ben Foster; same SSN but conflicting DOB.
  00710 <-> BEN-50: Arjun Das; same name/DOB but conflicting SSN.
Review and unmatched cases:
  BEN-48 DOB is 04/03/1985; payroll DOB is 1985-03-04. Preserve both claims.
  BEN-50 SSN ends 1019; payroll SSN ends 1010. Do not silently choose a value.
  BEN-43 dental says employee + spouse but includes a child. Flag inconsistency.
  00709 Tessa Watts appears only in payroll; absence is not an explicit waiver.
  BEN-51 Wendy Cole appears only in enrollment; coverage ended 2026-09-30.
  BEN-52 Alex Patel has no DOB/SSN: do not auto-merge into either Alex employee.
  Postal 7030 should be reviewed/normalized without losing other leading zeros.
Keep member/benefit records distinct: medical and dental rows are not duplicate people.

COMPANY E: STAFF + WIDE BENEFIT ELECTIONS + HOUSEHOLD
Staff: 8 employees; trim J-008's surrounding whitespace. Footer is not a person.
Benefits: 9 employee-election rows; J-003 has a separate future medical election.
Household: 11 dependent rows; two cannot be confidently attached to known staff.
Join known records by trimmed Worker Ref / Household Ref, then check identity.
Handle accents and punctuation: Lucia Garcia / Lucia with accents; Jose Muller /
Jose with accents; Eun Ji Park / Eun-ji Park. Do not collapse the two Jamie Walkers.
J-003 DOB 03/04/1985 in staff is 1985-04-03, because staff explicitly uses DD/MM/YYYY.
J-005 gets its missing DOB from benefit elections. J-006 still lacks an SSN.
J-001 medical covers employee, spouse Rafael, and children Sofia and Leo.
J-001 dental covers employee and children, not spouse; Leo starts 2026-10-01.
J-002 child Milo is a household member explicitly not enrolled in any benefit.
J-003 current medical remains separate from pending medical effective 2027-01-01.
Blank future dental/vision fields do not cancel existing records.
J-006 Asha has unknown DOB and unknown participation. Dental blank is not WAIVED.
J-007 has not elected benefits; do not convert that into an explicit waiver.
J-008 employee/spouse coverage ended 2026-10-15; not active at the snapshot date.
J-999 household has no matching employee: retain as unresolved, do not attach elsewhere.
Robin Walker: invalid 29/02/2023, end before start, medical tier/member mismatch.
Alex Walker: missing Household Ref and ambiguous Jamie Walker parent; needs review.
Staff postal 2108 has a dropped leading zero; salary formats differ and one is hourly.

These notes describe fixture intent. They are evaluation expectations, not hints
to include in the extractor's document inputs. Names alone are not matching keys.
