package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestDerivedColumnReferencesIncludeSelectAndConsumerSites(t *testing.T) {
	const text = `SELECT d.Id
FROM (
  SELECT SingerId AS Id FROM Singers GROUP BY Id
) AS d`
	h := newParsedTestHandler(t, "/test.sql", text)
	index := newTextIndex(text)
	consumerOffset := strings.Index(text, "d.Id") + len("d.")
	declarationOffset := strings.Index(text, " AS Id") + len(" AS ")
	groupOffset := strings.Index(text, "GROUP BY Id") + len("GROUP BY ")

	got, err := h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     index.position(consumerOffset),
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 ||
		got[0].Range.Start != index.position(declarationOffset) ||
		got[1].Range.Start != index.position(groupOffset) ||
		got[2].Range.Start != index.position(consumerOffset) {
		t.Fatalf("References() = %#v, want declaration, GROUP BY, and consumer", got)
	}

	highlights, err := h.DocumentHighlight(context.Background(), &protocol.DocumentHighlightParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     index.position(consumerOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(highlights) != 3 ||
		highlights[0].Kind != protocol.Write ||
		highlights[1].Kind != protocol.Read ||
		highlights[2].Kind != protocol.Read {
		t.Fatalf("DocumentHighlight() = %#v, want one declaration and two reads", highlights)
	}
}

func TestDerivedColumnReferencesRespectAliasBindings(t *testing.T) {
	const text = `SELECT outer_d.Id
FROM (SELECT 1 AS Id) AS outer_d
JOIN (SELECT 2 AS Id) AS inner_d ON outer_d.Id = inner_d.Id`
	h := newParsedTestHandler(t, "/test.sql", text)
	index := newTextIndex(text)
	outerReference := strings.Index(text, "outer_d.Id") + len("outer_d.")

	got, err := h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     index.position(outerReference),
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("References() = %#v, want outer declaration and two outer references", got)
	}
	innerDeclaration := strings.LastIndex(text, " AS Id") + len(" AS ")
	innerReference := strings.LastIndex(text, "inner_d.Id") + len("inner_d.")
	for _, location := range got {
		if location.Range.Start == index.position(innerDeclaration) ||
			location.Range.Start == index.position(innerReference) {
			t.Fatalf("References() included inner derived-table column: %#v", got)
		}
	}
}

func TestDocumentSnapshotStoresDerivedColumnIndex(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT d.Id FROM (SELECT 1 AS Id) AS d"
	snapshot := parseDocumentSnapshot(path, text, 1, 1, documentOriginOpen, nil)
	if snapshot.parseErr != nil {
		t.Fatal(snapshot.parseErr)
	}
	if len(snapshot.derivedColumns.bindings) != 1 ||
		snapshot.derivedColumns.bindings[0].name != "Id" ||
		len(snapshot.derivedColumns.bindings[0].referenceRanges) != 1 {
		t.Fatalf("snapshot derived columns = %#v, want Id declaration and consumer", snapshot.derivedColumns)
	}
}
