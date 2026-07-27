package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish"
)

func TestExplicitTableAliasNavigation(t *testing.T) {
	const path = "/test.sql"
	const text = `CREATE TABLE Singers (SingerId INT64, Name STRING(MAX)) PRIMARY KEY (SingerId);
SELECT s.SingerId FROM Singers AS s WHERE s.SingerId > 0`
	h := newParsedTestHandler(t, path, text)
	index := newTextIndex(text)
	aliasDeclarationOffset := strings.Index(text, "AS s") + len("AS ")
	firstAliasReferenceOffset := strings.Index(text, "s.SingerId")
	secondAliasReferenceOffset := strings.LastIndex(text, "s.SingerId")
	memberOffset := firstAliasReferenceOffset + len("s.")
	columnDeclarationOffset := strings.Index(text, "SingerId")
	columnTypeOffset := strings.Index(text, "INT64")
	uri := protocol.DocumentURI("file:///test.sql")

	definitions, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     index.position(firstAliasReferenceOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantAliasDeclaration := index.rangeByByteOffsets(aliasDeclarationOffset, aliasDeclarationOffset+1)
	if len(definitions) != 1 || definitions[0].Range != wantAliasDeclaration {
		t.Fatalf("Definition(alias) = %#v, want alias declaration %#v", definitions, wantAliasDeclaration)
	}

	references, err := h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     index.position(firstAliasReferenceOffset),
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 3 ||
		references[0].Range != wantAliasDeclaration ||
		references[1].Range.Start != index.position(firstAliasReferenceOffset) ||
		references[2].Range.Start != index.position(secondAliasReferenceOffset) {
		t.Fatalf("References(alias) = %#v, want declaration and two references", references)
	}

	highlights, err := h.DocumentHighlight(context.Background(), &protocol.DocumentHighlightParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     index.position(secondAliasReferenceOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(highlights) != 3 ||
		highlights[0].Kind != protocol.Write ||
		highlights[1].Kind != protocol.Read ||
		highlights[2].Kind != protocol.Read {
		t.Fatalf("DocumentHighlight(alias) = %#v, want one write and two reads", highlights)
	}

	definitions, err = h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     index.position(memberOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantColumnDeclaration := index.rangeByByteOffsets(columnDeclarationOffset, columnDeclarationOffset+len("SingerId"))
	if len(definitions) != 1 || definitions[0].Range != wantColumnDeclaration {
		t.Fatalf("Definition(member) = %#v, want column declaration %#v", definitions, wantColumnDeclaration)
	}

	types, err := h.TypeDefinition(context.Background(), &protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position:     index.position(memberOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantType := index.rangeByByteOffsets(columnTypeOffset, columnTypeOffset+len("INT64"))
	if len(types) != 1 || types[0].Range != wantType {
		t.Fatalf("TypeDefinition(member) = %#v, want schema type %#v", types, wantType)
	}
}

func TestExplicitTableAliasMemberNavigationAcrossDocuments(t *testing.T) {
	const query = "SELECT s.SingerId FROM Sales.Singers AS s"
	const schema = "CREATE TABLE Sales.Singers (SingerId INT64) PRIMARY KEY (SingerId)"
	h := newParsedTestHandler(t, "/query.sql", query)
	addParsedTestDocument(t, h, "/schema.sql", schema)
	memberPosition := newTextIndex(query).position(strings.Index(query, "SingerId"))

	definitions, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     memberPosition,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantColumn := newTextIndex(schema).rangeByByteOffsets(strings.Index(schema, "SingerId"), strings.Index(schema, "SingerId")+len("SingerId"))
	if len(definitions) != 1 || definitions[0].URI != "file:///schema.sql" || definitions[0].Range != wantColumn {
		t.Fatalf("Definition(member) = %#v, want schema column %#v", definitions, wantColumn)
	}

	types, err := h.TypeDefinition(context.Background(), &protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     memberPosition,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	typeOffset := strings.Index(schema, "INT64")
	wantType := newTextIndex(schema).rangeByByteOffsets(typeOffset, typeOffset+len("INT64"))
	if len(types) != 1 || types[0].URI != "file:///schema.sql" || types[0].Range != wantType {
		t.Fatalf("TypeDefinition(member) = %#v, want schema type %#v", types, wantType)
	}
}

func TestExplicitTableAliasRespectsNestedShadowing(t *testing.T) {
	const text = "SELECT s.SingerId, (SELECT s.AlbumId FROM Albums AS s) FROM Singers AS s"
	statements, err := memefish.ParseStatements("/test.sql", text)
	if err != nil {
		t.Fatal(err)
	}
	index := extractAliasIndex(newTextIndex(text), statements, extractCTEIndex(newTextIndex(text), statements))
	if len(index.bindings) != 2 {
		t.Fatalf("extractAliasIndex() returned %d bindings, want inner and outer s", len(index.bindings))
	}

	outerDeclarationOffset := strings.LastIndex(text, "AS s") + len("AS ")
	innerDeclarationOffset := strings.Index(text, "AS s") + len("AS ")
	outerReferenceOffset := strings.Index(text, "s.SingerId")
	innerReferenceOffset := strings.Index(text, "s.AlbumId")
	h := newParsedTestHandler(t, "/test.sql", text)

	tests := []struct {
		name              string
		referenceOffset   int
		declarationOffset int
	}{
		{name: "outer", referenceOffset: outerReferenceOffset, declarationOffset: outerDeclarationOffset},
		{name: "inner", referenceOffset: innerReferenceOffset, declarationOffset: innerDeclarationOffset},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
					Position:     newTextIndex(text).position(test.referenceOffset),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := newTextIndex(text).rangeByByteOffsets(test.declarationOffset, test.declarationOffset+1)
			if len(got) != 1 || got[0].Range != want {
				t.Fatalf("Definition() = %#v, want shadowed declaration %#v", got, want)
			}
		})
	}
}

func TestDuplicateExplicitTableAliasesFailClosed(t *testing.T) {
	const text = "SELECT a.SingerId FROM Singers AS a JOIN Albums AS a ON TRUE"
	h := newParsedTestHandler(t, "/test.sql", text)

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(strings.Index(text, "a.SingerId")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Definition() = %#v, want no result for duplicate alias", got)
	}
}

func TestCTESourceAliasDoesNotUseSameNamedPhysicalTableShape(t *testing.T) {
	const text = `CREATE TABLE LocalRows (PhysicalOnly INT64) PRIMARY KEY (PhysicalOnly);
WITH LocalRows AS (SELECT 1 AS LogicalOnly)
SELECT l.PhysicalOnly FROM LocalRows AS l`
	h := newParsedTestHandler(t, "/test.sql", text)

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(strings.LastIndex(text, "PhysicalOnly")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Definition() = %#v, want no physical-column result for CTE source", got)
	}
}
