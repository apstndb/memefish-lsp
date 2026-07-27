package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestDiagnosticReportsDuplicateTableAliases(t *testing.T) {
	const query = `SELECT a.SingerId
FROM Singers AS a JOIN Albums AS a ON TRUE`
	h := newParsedTestHandler(t, "/query.sql", query)

	got, err := h.Diagnostic(context.Background(), &protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	full := got.Value.(protocol.FullDocumentDiagnosticReport)
	if len(full.Items) != 1 {
		t.Fatalf("Diagnostic() items = %#v, want one duplicate table alias error", full.Items)
	}
	diagnostic := full.Items[0]
	index := newTextIndex(query)
	if diagnostic.Code != duplicateTableAliasDiagnosticCode ||
		diagnostic.Range.Start != index.position(strings.LastIndex(query, "AS a")+len("AS ")) ||
		len(diagnostic.RelatedInformation) != 1 ||
		diagnostic.RelatedInformation[0].Location.Range.Start !=
			index.position(strings.Index(query, "AS a")+len("AS ")) {
		t.Fatalf("Diagnostic() item = %#v, want duplicate and first alias ranges", diagnostic)
	}
}

func TestDiagnosticAllowsNestedTableAliasShadowing(t *testing.T) {
	const query = `SELECT a.SingerId,
  (SELECT a.AlbumId FROM Albums AS a)
FROM Singers AS a`
	h := newParsedTestHandler(t, "/query.sql", query)

	got, err := h.Diagnostic(context.Background(), &protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	full := got.Value.(protocol.FullDocumentDiagnosticReport)
	if len(full.Items) != 0 {
		t.Fatalf("Diagnostic() items = %#v, want nested alias shadowing to be valid", full.Items)
	}
}
