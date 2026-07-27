package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish"
)

func TestCTEBindingShadowsPhysicalTableForNavigation(t *testing.T) {
	const path = "/test.sql"
	const text = `CREATE TABLE LocalRows (Id INT64) PRIMARY KEY (Id);
WITH LocalRows AS (SELECT 1 AS Id) SELECT * FROM LocalRows`
	h := newParsedTestHandler(t, path, text)
	declarationOffset := strings.Index(text, "LocalRows AS")
	referenceOffset := strings.LastIndex(text, "LocalRows")
	declarationRange := newTextIndex(text).rangeByByteOffsets(declarationOffset, declarationOffset+len("LocalRows"))
	referencePosition := newTextIndex(text).position(referenceOffset + 1)

	definitions, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     referencePosition,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 || definitions[0].Range != declarationRange {
		t.Fatalf("Definition() = %#v, want CTE declaration %#v", definitions, declarationRange)
	}

	references, err := h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     referencePosition,
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 2 || references[0].Range != declarationRange || references[1].Range.Start != newTextIndex(text).position(referenceOffset) {
		t.Fatalf("References() = %#v, want CTE declaration and use", references)
	}
	references, err = h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     referencePosition,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 1 || references[0].Range.Start != newTextIndex(text).position(referenceOffset) {
		t.Fatalf("References() without declaration = %#v, want only CTE use", references)
	}
	physicalReferences, err := h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Character: 14},
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(physicalReferences) != 1 || physicalReferences[0].Range.Start.Line != 0 {
		t.Fatalf("physical-table References() = %#v, want declaration without CTE-bound use", physicalReferences)
	}

	highlights, err := h.DocumentHighlight(context.Background(), &protocol.DocumentHighlightParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     declarationRange.Start,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(highlights) != 2 || highlights[0].Kind != protocol.Write || highlights[1].Kind != protocol.Read {
		t.Fatalf("DocumentHighlight() = %#v, want write declaration and read reference", highlights)
	}
}

func TestCTEBindingRespectsDeclarationOrder(t *testing.T) {
	const path = "/test.sql"
	const text = "WITH A AS (SELECT 1), B AS (SELECT * FROM A) SELECT * FROM B"
	statements, err := memefish.ParseStatements(path, text)
	if err != nil {
		t.Fatal(err)
	}

	index := extractCTEIndex(newTextIndex(text), statements)
	if len(index.bindings) != 2 {
		t.Fatalf("extractCTEIndex() returned %d bindings, want A and B", len(index.bindings))
	}
	if index.bindings[0].name != "A" || len(index.bindings[0].referenceRanges) != 1 {
		t.Fatalf("A binding = %#v, want one reference from B body", index.bindings[0])
	}
	if index.bindings[1].name != "B" || len(index.bindings[1].referenceRanges) != 1 {
		t.Fatalf("B binding = %#v, want one reference from main query", index.bindings[1])
	}
}

func TestNestedCTEShadowsOuterAfterItsDeclaration(t *testing.T) {
	const path = "/test.sql"
	const text = "WITH T AS (SELECT 1), U AS (WITH T AS (SELECT * FROM T) SELECT * FROM T) SELECT * FROM U"
	statements, err := memefish.ParseStatements(path, text)
	if err != nil {
		t.Fatal(err)
	}
	index := extractCTEIndex(newTextIndex(text), statements)
	if len(index.bindings) != 3 {
		t.Fatalf("extractCTEIndex() returned %d bindings, want outer T, inner T, and U", len(index.bindings))
	}
	if len(index.bindings[0].referenceRanges) != 1 {
		t.Fatalf("outer T references = %#v, want inner CTE body reference", index.bindings[0].referenceRanges)
	}
	if len(index.bindings[1].referenceRanges) != 1 {
		t.Fatalf("inner T references = %#v, want inner main-query reference", index.bindings[1].referenceRanges)
	}

	h := newParsedTestHandler(t, path, text)
	lastReference := strings.LastIndex(text, "FROM T") + len("FROM ")
	definitions, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(lastReference),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstDeclaration := strings.Index(text, "T AS")
	innerDeclaration := strings.Index(text[firstDeclaration+1:], "T AS") + firstDeclaration + 1
	want := newTextIndex(text).rangeByByteOffsets(innerDeclaration, innerDeclaration+1)
	if len(definitions) != 1 || definitions[0].Range != want {
		t.Fatalf("Definition() = %#v, want inner T declaration %#v", definitions, want)
	}
}

func TestCTEBindingDoesNotLeakAcrossStatements(t *testing.T) {
	const path = "/test.sql"
	const text = "WITH T AS (SELECT 1) SELECT * FROM T;\nSELECT * FROM T"
	statements, err := memefish.ParseStatements(path, text)
	if err != nil {
		t.Fatal(err)
	}

	index := extractCTEIndex(newTextIndex(text), statements)
	if len(index.bindings) != 1 || len(index.bindings[0].referenceRanges) != 1 {
		t.Fatalf("extractCTEIndex() = %#v, want only first-statement T reference", index)
	}
}

func TestDefinitionResolvesCTEAliasColumns(t *testing.T) {
	const text = `WITH LocalRows AS (
  SELECT SingerId AS Id, Name FROM Singers
)
SELECT r.Id, r.Name FROM LocalRows AS r`
	h := newParsedTestHandler(t, "/test.sql", text)
	index := newTextIndex(text)

	tests := []struct {
		name              string
		columnName        string
		referenceOffset   int
		declarationOffset int
	}{
		{
			name:              "explicit",
			columnName:        "Id",
			referenceOffset:   strings.LastIndex(text, "r.Id") + len("r."),
			declarationOffset: strings.Index(text, " AS Id") + len(" AS "),
		},
		{
			name:              "implicit",
			columnName:        "Name",
			referenceOffset:   strings.LastIndex(text, "r.Name") + len("r."),
			declarationOffset: strings.Index(text, "Name"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
					Position:     index.position(test.referenceOffset),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := index.rangeByByteOffsets(
				test.declarationOffset,
				test.declarationOffset+len(test.columnName),
			)
			if len(got) != 1 || got[0].Range != want {
				t.Fatalf("Definition() = %#v, want CTE column %#v", got, want)
			}
		})
	}
}

func TestHoverDescribesCTEAliasColumn(t *testing.T) {
	const text = `WITH LocalRows AS (SELECT SingerId AS Id FROM Singers)
SELECT r.Id FROM LocalRows AS r`
	h := newParsedTestHandler(t, "/test.sql", text)
	memberOffset := strings.LastIndex(text, "r.Id") + len("r.")

	got, err := h.Hover(context.Background(), &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(memberOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !strings.Contains(got.Contents.Value, "**CTE column** `LocalRows.Id`") ||
		!strings.Contains(got.Contents.Value, "SingerId AS Id") {
		t.Fatalf("Hover() = %#v, want CTE output expression", got)
	}
}

func TestCTEAliasColumnNavigationRejectsUnknownShape(t *testing.T) {
	const text = `WITH LocalRows AS (SELECT * FROM Singers)
SELECT r.Id FROM LocalRows AS r`
	h := newParsedTestHandler(t, "/test.sql", text)

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(strings.LastIndex(text, "r.Id") + len("r.")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Definition() = %#v, want none for SELECT * CTE shape", got)
	}
}
