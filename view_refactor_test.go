package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestRenameEditsAllKnownViewSites(t *testing.T) {
	const schema = `CREATE VIEW ActiveSingers SQL SECURITY INVOKER AS
SELECT * FROM Singers;
DROP VIEW ActiveSingers;
GRANT SELECT ON VIEW ActiveSingers TO ROLE reader`
	const query = "SELECT * FROM ActiveSingers"
	h := newParsedTestHandler(t, "/schema.sql", schema)
	addParsedTestDocument(t, h, "/query.sql", query)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     newTextIndex(query).position(strings.Index(query, "ActiveSingers")),
		},
		NewName: "CurrentSingers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("Rename() returned nil, want complete view edit")
	}
	if len(got.Changes["file:///schema.sql"]) != 3 || len(got.Changes["file:///query.sql"]) != 1 {
		t.Fatalf("Rename() = %#v, want CREATE/DROP/GRANT and query edits", got)
	}
	for _, edits := range got.Changes {
		for _, edit := range edits {
			if edit.NewText != "CurrentSingers" {
				t.Fatalf("Rename() edit NewText = %q, want CurrentSingers", edit.NewText)
			}
		}
	}
}

func TestRenameViewExcludesCTEShadowing(t *testing.T) {
	const query = "SELECT * FROM ActiveSingers"
	h := newParsedTestHandler(t, "/schema.sql", activeSingersViewDDL)
	addParsedTestDocument(t, h, "/query.sql", query)
	addParsedTestDocument(t, h, "/shadowed.sql", `WITH ActiveSingers AS (SELECT 1)
SELECT * FROM ActiveSingers`)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     newTextIndex(query).position(strings.Index(query, "ActiveSingers")),
		},
		NewName: "CurrentSingers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Changes) != 2 {
		t.Fatalf("Rename() = %#v, want declaration and unshadowed query edits", got)
	}
	if _, ok := got.Changes["file:///shadowed.sql"]; ok {
		t.Fatalf("Rename() changes = %#v, want CTE-bound reference excluded", got.Changes)
	}
}

func TestRenameViewFailsClosedForDuplicateDeclarations(t *testing.T) {
	const text = `CREATE VIEW ActiveSingers SQL SECURITY INVOKER AS SELECT 1;
CREATE VIEW ActiveSingers SQL SECURITY INVOKER AS SELECT 2`
	h := newParsedTestHandler(t, "/schema.sql", text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///schema.sql"},
			Position:     newTextIndex(text).position(strings.Index(text, "ActiveSingers")),
		},
		NewName: "CurrentSingers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want nil for duplicate view declarations", got)
	}
}

func TestRenameViewFailsClosedForQuotedReference(t *testing.T) {
	const query = "SELECT * FROM `ActiveSingers`"
	h := newParsedTestHandler(t, "/schema.sql", activeSingersViewDDL)
	addParsedTestDocument(t, h, "/query.sql", query)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///schema.sql"},
			Position:     newTextIndex(activeSingersViewDDL).position(strings.Index(activeSingersViewDDL, "ActiveSingers")),
		},
		NewName: "CurrentSingers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want nil for quoted view reference", got)
	}
}

func TestRenameViewRejectsRelationNameConflict(t *testing.T) {
	const query = "SELECT * FROM ActiveSingers"
	h := newParsedTestHandler(t, "/schema.sql", activeSingersViewDDL)
	addParsedTestDocument(t, h, "/table.sql", "CREATE TABLE CurrentSingers (Id INT64) PRIMARY KEY (Id)")
	addParsedTestDocument(t, h, "/query.sql", query)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     newTextIndex(query).position(strings.Index(query, "ActiveSingers")),
		},
		NewName: "CurrentSingers",
	})
	if err == nil || got != nil {
		t.Fatalf("Rename() = %#v, %v; want relation conflict error", got, err)
	}
}

func TestPrepareRenameAndLinkedEditingRangeForView(t *testing.T) {
	const text = activeSingersViewDDL + ";\nSELECT * FROM ActiveSingers"
	h := newParsedTestHandler(t, "/test.sql", text)
	position := newTextIndex(text).position(strings.LastIndex(text, "ActiveSingers"))

	prepared, err := h.PrepareRename(context.Background(), &protocol.PrepareRenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     position,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared == nil || prepared.Placeholder != "ActiveSingers" {
		t.Fatalf("PrepareRename() = %#v, want ActiveSingers", prepared)
	}

	linked, err := h.LinkedEditingRange(context.Background(), &protocol.LinkedEditingRangeParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     position,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if linked == nil || len(linked.Ranges) != 2 {
		t.Fatalf("LinkedEditingRange() = %#v, want declaration and reference", linked)
	}
}
