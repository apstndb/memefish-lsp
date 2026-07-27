package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestCodeLensCountsLexicalCTEReferences(t *testing.T) {
	const text = `WITH A AS (SELECT 1 AS Id),
B AS (SELECT Id FROM A)
SELECT a.Id FROM A AS a JOIN B AS b ON a.Id = b.Id`
	h := newParsedTestHandler(t, "/query.sql", text)

	got, err := h.CodeLens(context.Background(), &protocol.CodeLensParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("CodeLens() = %#v, want A and B reference lenses", got)
	}
	if got[0].Command == nil || got[0].Command.Title != "2 references" ||
		got[1].Command == nil || got[1].Command.Title != "1 reference" {
		t.Fatalf("CodeLens() = %#v, want A=2 and B=1", got)
	}
	var firstReference protocol.Location
	if err := json.Unmarshal(got[0].Command.Arguments[0], &firstReference); err != nil {
		t.Fatal(err)
	}
	if firstReference.URI != "file:///query.sql" ||
		firstReference.Range.Start != newTextIndex(text).position(
			len("WITH A AS (SELECT 1 AS Id),\nB AS (SELECT Id FROM "),
		) {
		t.Fatalf("first A reference = %#v", firstReference)
	}
}

func TestCodeLensSeparatesShadowedCTEReferences(t *testing.T) {
	const text = `WITH T AS (SELECT 1 AS Id),
U AS (
  WITH T AS (SELECT 2 AS Id)
  SELECT Id FROM T
)
SELECT Id FROM T`
	h := newParsedTestHandler(t, "/query.sql", text)

	got, err := h.CodeLens(context.Background(), &protocol.CodeLensParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("CodeLens() = %#v, want outer and inner T lenses", got)
	}
	for _, lens := range got {
		if lens.Command == nil || lens.Command.Title != "1 reference" {
			t.Fatalf("CodeLens() = %#v, want one reference per T binding", got)
		}
	}
}
