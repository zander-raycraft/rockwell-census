SYNTHETIC CENSUS TEST FIXTURES - BATCH 02

24 additional files across eight fictional companies (F through M).
Snapshot: 2026-10-31. All people, companies, plans and identifiers are fictional.
Synthetic SSNs use 9xx prefixes; production SSN validity is a separate concern.
The earlier examples and first synthetic batch remain unchanged.

COMPANIES AND FILE GROUPS
F: Willow Loom Supply, 18 employees; payroll + enrollment + household.
G: Ember Finch Design, 32 employees; payroll + enrollment + household.
H: Silver Parcel Works, 45 employees; payroll + enrollment + household.
I: Cedar Ribbon Labs, 60 employees; separate medical, dental and vision reports.
J: Moss Lantern Goods, 80 employees; handwritten census + identity supplement + change requests.
K: Saffron Wren Studio, 120 employees; payroll + enrollment + household.
L: Blue Thread Services, 180 employees; reordered benefit report + payroll + household.
M: Copper Orchard Operations, 1200 employees; volume-test payroll + enrollment + household.

FORMATS
Comma, semicolon and pipe delimiters; header records vary; selected files include
UTF-8 BOMs, metadata, blank records, repeated headers, report footers or stale totals.
Dates explicitly use MM/DD/YYYY, DD/MM/YYYY or ISO as listed in manifest.json.
Company I's three reports intentionally use different date conventions and source ID namespaces.
All fields are strings. Source whitespace, leading zeros, accents and quoted commas are preserved.
Every file has intentional irregularities, with normal records alongside them.

EXPECTED BEHAVIOR
Ignore metadata, repeated headers and footers as employee/member records.
Deduplicate exact rows without collapsing separate benefits, dates or family members.
Payroll Worker No and benefits Subscriber Ref are different identifier namespaces.
Worker No collision, Alex Morgan name ambiguity, and identical-name/DOB Jordan Ellis
employees require identity evidence beyond a single field. Never match across companies.
Payroll Worker No values intentionally repeat across different companies.
Missing values are unknown, not explicit waivers. New hire is not an enrollment status.
Validate bad calendar dates, missing active plan, tier/member mismatch and inverted dates.
Keep source claims for conflicting DOBs, SSNs and dependent dates for human review.
Keep historical terminated and future pending elections separate from current coverage.
Do not apply unapproved change requests as confirmed enrollment updates.
Company J blanks do not establish dependent coverage; row order only gives household context.

MANIFEST
manifest.json lists each file's delimiter, encoding, header CSV record, columns,
detail CSV record numbers, expected issues, and per-record fixture identity labels.
CSV record numbers are one-based logical records, not necessarily physical line numbers.
Identity labels describe the synthetic author's intended person for evaluation.
They do not imply the visible source evidence is sufficient for an automatic match.
requiresIdentityReview explicitly marks intentionally ambiguous benefit records.
Issue records identify evidence to preserve or review, not fabricated corrected values.
Use the manifest for evaluation only; do not feed it into the document extractor.
