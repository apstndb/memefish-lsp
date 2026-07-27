package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestTypeDefinitionFollowsDerivedPassThroughColumn(t *testing.T) {
	const query = `SELECT d.Id
FROM (SELECT s.SingerId AS Id FROM Singers AS s) AS d`
	const schema = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)"
	h := newParsedTestHandler(t, "/query.sql", query)
	addParsedTestDocument(t, h, "/schema.sql", schema)

	got, err := h.TypeDefinition(context.Background(), &protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position: newTextIndex(query).position(
				strings.Index(query, "d.Id") + len("d."),
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := schemaTypeLocation(schema)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("TypeDefinition() = %#v, want %#v", got, want)
	}
}

func TestTypeDefinitionFollowsCTEPassThroughColumn(t *testing.T) {
	const query = `WITH LocalRows AS (
  SELECT s.SingerId AS Id FROM Singers AS s
)
SELECT r.Id FROM LocalRows AS r`
	const schema = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)"
	h := newParsedTestHandler(t, "/query.sql", query)
	addParsedTestDocument(t, h, "/schema.sql", schema)

	got, err := h.TypeDefinition(context.Background(), &protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position: newTextIndex(query).position(
				strings.LastIndex(query, "r.Id") + len("r."),
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := schemaTypeLocation(schema)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("TypeDefinition() = %#v, want %#v", got, want)
	}
}

func TestTypeDefinitionFollowsViewPassThroughColumn(t *testing.T) {
	const query = "SELECT v.Id FROM ActiveSingers AS v"
	const schema = `CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);
CREATE VIEW ActiveSingers SQL SECURITY INVOKER AS
SELECT s.SingerId AS Id FROM Singers AS s`
	h := newParsedTestHandler(t, "/query.sql", query)
	addParsedTestDocument(t, h, "/schema.sql", schema)

	got, err := h.TypeDefinition(context.Background(), &protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position: newTextIndex(query).position(
				strings.Index(query, "v.Id") + len("v."),
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := schemaTypeLocation(schema)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("TypeDefinition() = %#v, want %#v", got, want)
	}
}

func TestTypeDefinitionRejectsComputedQueryColumn(t *testing.T) {
	const query = "SELECT d.Id FROM (SELECT SingerId + 1 AS Id FROM Singers) AS d"
	h := newParsedTestHandler(t, "/query.sql", query)
	addParsedTestDocument(t, h, "/schema.sql", "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)")

	got, err := h.TypeDefinition(context.Background(), &protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position: newTextIndex(query).position(
				strings.Index(query, "d.Id") + len("d."),
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("TypeDefinition() = %#v, want none for computed column", got)
	}
}

func schemaTypeLocation(schema string) protocol.Location {
	offset := strings.Index(schema, "INT64")
	return protocol.Location{
		URI: "file:///schema.sql",
		Range: newTextIndex(schema).rangeByByteOffsets(
			offset,
			offset+len("INT64"),
		),
	}
}
