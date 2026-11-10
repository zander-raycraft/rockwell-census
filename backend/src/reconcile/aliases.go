package reconcile

import (
	"census/src/extractor"
	"census/src/parse"
)

/*
Document, Parsed, Plan, and the extraction maps are the types shared with parse and extractor.

* @brief: aliases -> names from parse and extractor used by reconciliation
*/
type (
	Document        = parse.Document
	Parsed          = parse.Parsed
	Plan            = extractor.Plan
	FieldMap        = extractor.FieldMap
	LabelMap        = extractor.LabelMap
	EmailResolution = extractor.EmailResolution
)
