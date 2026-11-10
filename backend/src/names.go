package main

import (
	"census/src/extractor"
	"census/src/parse"
	"census/src/reconcile"
)

/*
The server keeps the short names for the types owned by parse, extractor, and reconcile.

* @brief: aliases -> names used by the HTTP server and its pipeline test
*/
type (
	Document  = parse.Document
	Parsed    = parse.Parsed
	Plan      = extractor.Plan
	FieldMap  = extractor.FieldMap
	State     = reconcile.State
	DB        = reconcile.DB
	OpenIssue = reconcile.OpenIssue
	SourceRef = reconcile.SourceRef
	Formatted = reconcile.Formatted
)
