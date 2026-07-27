package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish"
)

func TestExtractDDLFactsPreservesDuplicateStructuredTableDeclarations(t *testing.T) {
	const path = "/schema.sql"
	const text = `CREATE TABLE Sales.Singers (SingerId INT64) PRIMARY KEY (SingerId);
CREATE TABLE Sales.Singers (SingerId INT64) PRIMARY KEY (SingerId)`
	statements, err := memefish.ParseStatements(path, text)
	if err != nil {
		t.Fatal(err)
	}

	facts := extractDDLFacts(newTextIndex(text), statements)
	if len(facts.tables) != 2 || len(facts.symbols) != 2 {
		t.Fatalf("extractDDLFacts() = %d tables, %d symbols; want two duplicate-preserving declarations", len(facts.tables), len(facts.symbols))
	}
	for i, table := range facts.tables {
		if table.name.string() != "Sales.Singers" || len(table.name.segments) != 2 {
			t.Fatalf("table %d name = %#v, want structured Sales.Singers", i, table.name)
		}
		if len(table.columns) != 1 || table.columns[0].name.string() != "SingerId" || table.columns[0].schemaType == nil {
			t.Fatalf("table %d columns = %#v, want known SingerId shape", i, table.columns)
		}
	}
	if facts.tables[0].name.segments[0].selectionRange.Start != (protocol.Position{Character: 13}) ||
		facts.tables[0].name.segments[1].selectionRange.Start != (protocol.Position{Character: 19}) {
		t.Fatalf("structured segment ranges = %#v", facts.tables[0].name.segments)
	}
}

func TestDocumentSymbolUsesTypedDDLFacts(t *testing.T) {
	const path = "/schema.sql"
	const text = `CREATE SCHEMA Sales;
CREATE TABLE Sales.Singers (SingerId INT64) PRIMARY KEY (SingerId);
CREATE VIEW Sales.ActiveSingers SQL SECURITY INVOKER
AS SELECT * FROM Sales.Singers`
	h := newParsedTestHandler(t, path, text)

	got, err := h.DocumentSymbol(context.Background(), &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///schema.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("DocumentSymbol() returned %d symbols, want schema, table, and view: %#v", len(got), got)
	}
	table, ok := got[1].(protocol.DocumentSymbol)
	if !ok {
		t.Fatalf("DocumentSymbol()[1] = %T, want protocol.DocumentSymbol", got[1])
	}
	if table.Name != "Sales.Singers" || table.Kind != protocol.Struct || len(table.Children) != 1 {
		t.Fatalf("table symbol = %#v", table)
	}
	if table.SelectionRange.Start.Line != 1 || !rangeContains(table.Range, table.SelectionRange) {
		t.Fatalf("table selection range = %#v, declaration range = %#v", table.SelectionRange, table.Range)
	}
	column := table.Children[0]
	if column.Name != "SingerId" || column.Kind != protocol.Field || !rangeContains(column.Range, column.SelectionRange) {
		t.Fatalf("column symbol = %#v", column)
	}
	view := got[2].(protocol.DocumentSymbol)
	if view.Name != "Sales.ActiveSingers" || view.Kind != protocol.Object || !rangeContains(view.Range, view.SelectionRange) {
		t.Fatalf("view symbol = %#v", view)
	}
}

func TestWorkspaceSymbolPreservesDuplicateDeclarations(t *testing.T) {
	const path = "/schema.sql"
	const text = `CREATE TABLE Sales.Singers (SingerId INT64) PRIMARY KEY (SingerId);
CREATE TABLE Sales.Singers (SingerId INT64) PRIMARY KEY (SingerId)`
	h := newParsedTestHandler(t, path, text)

	got, err := h.Symbol(context.Background(), &protocol.WorkspaceSymbolParams{Query: "Sales.Singers"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Symbol() returned %d duplicate table declarations, want 2: %#v", len(got), got)
	}
	if got[0].Location.Range.Start.Line != 0 || got[1].Location.Range.Start.Line != 1 {
		t.Fatalf("Symbol() ranges = %#v, want source-ordered duplicate declarations", got)
	}
}

func TestDocumentSnapshotStoresDDLFacts(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	h.SetClient(&recordingClient{})

	if err := h.DidOpen(context.Background(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:     "file:///schema.sql",
			Version: 1,
			Text:    "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)",
		},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := h.documentSnapshot("/schema.sql")
	if snapshot == nil || len(snapshot.facts.tables) != 1 || snapshot.facts.tables[0].name.string() != "Singers" {
		t.Fatalf("snapshot facts = %#v, want Singers table", snapshot)
	}
}
