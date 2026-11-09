# Census audit

Two passes of every example file, companies A through M, on 8–9 Nov 2026. Each pass used its own company. The model was gpt-6-luna. Source: the local Postgres trial companies and the step logs under `backend/logs`.

The shape of one document in and one state out is met, and the step log is met. The census that comes out is not one a person could check. Company B is two rosters. Company C stored every coverage fact as empty. The same file run twice does not produce the same people or the same issues.

| | |
|---|---|
| Hard spec, verified | 4/10 |
| Company B employees | 17 vs 9 |
| Issues resolved | 0 |
| Company results checked | 26 |

Provenance is out of scope and was not built. Fixture notes in `examples/synthetic` were used to score results. They were not sent to the model. Trial companies are `T1 Company A` through `M` and `T2 Company A` through `M`.

## Hard spec

Checked against `portotype-specs.md`.

| Requirement | Result | What was verified |
|---|---|---|
| Local only, no deployment | Met | Docker Compose on this machine. Curl against 127.0.0.1:8080. No frontend, which the spec puts after the pipeline works. |
| One document plus current state returns the next state | Met | Employees, dependents, enrollments, and open issues commit together. Checked on every file, twice. |
| A document may raise issues, resolve them, or do neither | Partial | Issues were raised. A note with no facts changed nothing. No stored issue was resolved. All 4,658 trial issues stayed open. |
| An email can say two named people are the same person | Partial | An email is accepted and parsed. A same-person reply has not been shown to merge anyone. |
| Messy documents come out as a sensible census | Missed | Company B stored 17 employees after enrollment, against 9. Company C stored 24 enrollments with no status and no tier. |
| The state can be checked, including on a second run | Missed | Company A stored 7 employees, then 8. Household issue counts moved by hundreds while the log summary still looked fine. |
| A complete step log while the run happens | Met, with a hole | Read, parse, extract, format, map, and write are on disk as each step finishes. The summary line can match while the column destinations differ. |
| A new private example should be adjustable | Missed | Column meaning depends on the model returning the same plan. Two passes of the same file did not. |

## What the pipeline covers

These held on both passes unless a row says otherwise.

| Behavior | Evidence |
|---|---|
| Parse of messy tables | Commas, semicolons, pipes, headers not on the first row, titles, and footers. Stable both passes. |
| One company only | Trial 1 and trial 2 each kept a separate roster. |
| Match when both sides have an SSN | Company D linked Liz Bennett to Elizabeth, kept the two Alex Patels apart, and kept Sean O'Neill and Isabel Ruiz-Martinez together. |
| A real conflict stays open | Benjamin Foster's payroll date and Arjun Das's payroll SSN were kept. The other claim stayed in an open issue. |
| Two people who share a name and not a date | The two Jamie Walkers in Company E stayed two people. |
| A blank cell stays unknown | Dev Singh's blank dental was not stored as waived. |
| An impossible date is not guessed | Jordan Reed 02/30/1988 and Robin Walker 29/02/2023 stayed empty, with an invalid-date issue. |
| A large file finishes | Company M, about 1,200 employees and 5,500 enrollment rows. Match about 1.5 seconds. The model call was most of the run. |
| A few coverage facts landed | Grace Kim's medical waiver. Wendy Cole's medical, terminated through 2026-09-30. Accents on Lucía García and José Müller. |

## Headcount after the full file sequence

Expected employees are the fixture headcounts. Payroll is the count after the first payroll or census file, before enrollment. Payroll-first cells are trial 1 / trial 2. F has one Jordan Ellis row, so its payroll count matched.

| Company | Expected | Trial 1 | Trial 2 | Payroll first | What repeated |
|---|---:|---:|---:|---|---|
| A | 7 | 7 | 8 | 7 / 8 | Sofia Ruiz was a dependent, then an employee |
| B | 9 | 17 | 17 | 9 / 9 | Eight copies. Same 17 both times |
| C | 7 | 8 | 7 | 8 / 7 | Eli D'Angelo flipped. All 24 coverage rows empty |
| D | 11 | 12 | 12 | 10 / 10 | SSN matches held. Alex Patel with no date was inserted |
| E | 8 | 9 | 9 | 8 / 8 | Eun-ji Park split in two. Both Jamies kept |
| F | 18 | 22 | 22 | 18 / 18 | Payroll exact. Four extras after enrollment |
| G | 32 | 35 | 35 | 31 / 31 | Two Jordan Ellis rows became one, then four extras |
| H | 45 | 48 | 48 | 44 / 44 | Same Jordan collapse, then four extras |
| I | 60 | 61 | 61 | 59 / 59 | Medical, dental, vision grew 59, 60, 61 |
| J | 80 | 89 | 89 | 79 / 79 | Change requests added all 10 rows as employees |
| K | 120 | 123 | 123 | 119 / 119 | Jordan collapse. Issues 409 vs 42 |
| L | 180 | 183 | 183 | 179 / 179 | Jordan collapse, then four extras |
| M | 1200 | 1203 | 1203 | 1199 / 1199 | Jordan collapse. 854 dependents unmatched |

## What moved between the two passes

Employee counts for B, D, and F through M matched. The plan's column destinations did not. Temperature was 0. Parse matched every time.

| File | Trial 1 | Trial 2 | Stored change |
|---|---|---|---|
| A census | 18 mappings, relationships filled | 14 mappings, relationships blank | 7 employees and 6 dependents, then 8 and 5. Sofia became an employee. |
| C census | 11 mappings, 7 labels | 12 mappings, 36 labels | 8 employees then 7. Eli was an employee, then Luca's dependent. 24 empty coverage rows both times. |
| E household | Dependent coverage written | Those rows omitted | 38 enrollments, then 20. Trial 1 stored the letter N as waived. |
| J handwritten census | 4 row issues | 228 row issues | Issues 39 vs 264. Benefit headers were also mapped onto status, and EE and FAM were rejected. |
| K household | Medical, dental, vision sent to enrollment type | Same headers sent to enrollment status | Both extract lines say 11 mappings and 4 labels. Issues 409 vs 42. Enrollments 543 vs 709. |
| M household | Y and N stored as enrolled and waived | Y and N written into the plan name | Employees stayed 1,203. Enrollment conflicts 2 vs 563. |

## Cases missing across files

Each row is a miss that showed up on more than one file, or on both passes of the file that contains it.

| Case | Where | Both passes |
|---|---|---|
| Short name, same date of birth, no SSN becomes a second employee. Coverage and dependents sit on the copy. | B: Bob/Robert, Maria Garcia/Garcia-Lopez, Jonathon/Jonathan, both Chris Lees, Kate/Katherine. Linda, Lucas, Daniel, Mia, and Ethan were on the copies. | Yes. 17 employees both times. |
| Same name, no SSN, date written in the other order becomes a second person. No conflict is opened. | B: Samuel Okonkwo, payroll 1985-04-03 and enrollment 1985-03-04. | Yes. |
| Same name and same date of birth with different SSNs collapses to one employee. | Payroll for G, H, K, L, and M. Two Jordan Ellis rows stored as one. Headcount short by 1 before any later file. | Yes. F has one Jordan and its payroll count was right. |
| No date of birth and no SSN inserts another employee. | D: Alex Patel. F through M: three Alex Morgan rows with neither identifier. E: Eun-ji Park with no date, and Eun Ji Park with a date, stored as two people. | Yes. |
| A blank relationship, or one that exists only in a note, flips between employee and dependent. | A: Sofia Ruiz. C: Eli D'Angelo. J: Remy Walker's relationship stayed empty. The note says he is a child. | A and C flipped. J was empty both times. |
| Census coverage is stored with no status and no tier. Waived, enrolled, pending, and not enrolled are the same value. | C: all 24 enrollments. A: 14 of 18 statuses. J: nearly all enrollment statuses. | Yes. |
| A status cell with extra words is dropped. | B: Derek Olson, Terminated 09/30/2026, no status and no end date. Leave dropped for Grace Kim, Marcus Brooks, and the on-leave Jamie. | Yes. |
| The letter N is stored as waived when the model labels it that way. A blank stays blank. | E household. Trial 1 stored N as waived. Trial 2 omitted the dependent coverage rows. Dev Singh's blank dental stayed absent. | Split. |
| The model picks different columns on the second pass. Headcount often stays. Issues and enrollments do not. | K, J, M, E, and the relationship flip on A and C. | Split. |
| A change-request file is applied as new people. | J: 79 employees after the census and the identity supplement, then 89 after 10 change-request rows. | Yes. |
| A later household file cannot find the parent when the name is shared, or when the subscriber name differs from the payroll name. | M: 854 unmatched dependents. E: Hana Park and Robin Walker unmatched. B: the family attached to the nickname row. | Yes. |
| Payroll worker numbers and benefit subscriber ids are discarded, so a later file cannot reuse a match. | I grew 59, then 60, then 61 against 60. Company M's household file could not use the subscriber ref the enrollment file had already resolved. | Yes. |
| After payroll plus enrollment, the large companies gain the same four extra employees. | F 18 to 22, G 31 to 35, H 44 to 48, K 119 to 123, L 179 to 183, M 1199 to 1203. | Yes. |

## What matching the nicknames would still leave wrong

Moving Bob onto Robert, and the other Company B pairs, would put Linda, Lucas, Daniel, Mia, and Ethan on the payroll people. It would not fill their empty relationships, give Derek an end date, or record leave for Grace, Marcus, or Jamie. Company C would still have no coverage facts. Company J's change requests would still add ten employees. Company M would still have 854 unmatched dependents, because the parent lookup does not remember a subscriber id from the earlier file. Dropping every slash date that is valid in both orders would also erase Jamie Walker's 1985-04-03, which both trials got right from a file that says day-month-year, and it would erase the Benjamin Foster conflict Company D already opens.
