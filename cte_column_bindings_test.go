package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestCTEColumnReferencesIncludeSelectAndConsumerSites(t *testing.T) {
	const text = `WITH LocalRows AS (
  SELECT SingerId AS Id FROM Singers GROUP BY Id
)
SELECT r.Id FROM LocalRows AS r`
	h := newParsedTestHandler(t, "/test.sql", text)
	index := newTextIndex(text)
	consumerOffset := strings.LastIndex(text, "r.Id") + len("r.")
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

func TestCTEColumnReferencesRespectNestedBindings(t *testing.T) {
	const text = `WITH T AS (SELECT 1 AS Id),
U AS (
  WITH T AS (SELECT 2 AS Id)
  SELECT inner_t.Id FROM T AS inner_t
)
SELECT outer_t.Id FROM T AS outer_t`
	h := newParsedTestHandler(t, "/test.sql", text)
	outerReference := strings.LastIndex(text, "outer_t.Id") + len("outer_t.")

	got, err := h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(outerReference),
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("References() = %#v, want outer declaration and reference only", got)
	}
	innerDeclaration := strings.Index(text[strings.Index(text, "U AS"):], "SELECT 2 AS Id") +
		strings.Index(text, "U AS") + len("SELECT 2 AS ")
	if got[0].Range.Start == newTextIndex(text).position(innerDeclaration) {
		t.Fatalf("References() included inner CTE declaration: %#v", got)
	}
}

func TestDocumentSnapshotStoresCTEColumnIndex(t *testing.T) {
	const path = "/test.sql"
	const text = `WITH LocalRows AS (SELECT 1 AS Id)
SELECT r.Id FROM LocalRows AS r`
	snapshot := parseDocumentSnapshot(path, text, 1, 1, documentOriginOpen, nil)
	if snapshot.parseErr != nil {
		t.Fatal(snapshot.parseErr)
	}
	if len(snapshot.cteColumns.bindings) != 1 ||
		snapshot.cteColumns.bindings[0].name != "Id" ||
		len(snapshot.cteColumns.bindings[0].referenceRanges) != 1 {
		t.Fatalf("snapshot CTE columns = %#v, want Id declaration and consumer", snapshot.cteColumns)
	}
}
