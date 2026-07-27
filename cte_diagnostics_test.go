package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestDiagnosticReportsDuplicateCTEDeclarations(t *testing.T) {
	const query = `WITH T AS (SELECT 1 AS Id),
T AS (SELECT 2 AS Id)
SELECT Id FROM T`
	h := newParsedTestHandler(t, "/query.sql", query)

	got, err := h.Diagnostic(context.Background(), &protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	full := got.Value.(protocol.FullDocumentDiagnosticReport)
	if len(full.Items) != 1 {
		t.Fatalf("Diagnostic() items = %#v, want one duplicate CTE error", full.Items)
	}
	diagnostic := full.Items[0]
	index := newTextIndex(query)
	if diagnostic.Code != duplicateCTEDiagnosticCode ||
		diagnostic.Range.Start != index.position(strings.LastIndex(query, "T AS")) ||
		len(diagnostic.RelatedInformation) != 1 ||
		diagnostic.RelatedInformation[0].Location.Range.Start !=
			index.position(strings.Index(query, "T AS")) {
		t.Fatalf("Diagnostic() item = %#v, want duplicate and first declaration ranges", diagnostic)
	}
}

func TestDiagnosticAllowsNestedCTEShadowing(t *testing.T) {
	const query = `WITH T AS (SELECT 1 AS Id),
U AS (
  WITH T AS (SELECT 2 AS Id)
  SELECT Id FROM T
)
SELECT Id FROM T`
	h := newParsedTestHandler(t, "/query.sql", query)

	got, err := h.Diagnostic(context.Background(), &protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	full := got.Value.(protocol.FullDocumentDiagnosticReport)
	if len(full.Items) != 0 {
		t.Fatalf("Diagnostic() items = %#v, want nested CTE shadowing to be valid", full.Items)
	}
}
