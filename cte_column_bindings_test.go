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

func TestRenameExplicitCTEColumn(t *testing.T) {
	const text = `WITH LocalRows AS (
  SELECT SingerId AS Id FROM Singers GROUP BY Id
)
SELECT r.Id FROM LocalRows AS r`
	h := newParsedTestHandler(t, "/test.sql", text)
	position := newTextIndex(text).position(strings.LastIndex(text, "r.Id") + len("r."))

	prepared, err := h.PrepareRename(context.Background(), &protocol.PrepareRenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     position,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared == nil || prepared.Placeholder != "Id" {
		t.Fatalf("PrepareRename() = %#v, want explicit CTE column Id", prepared)
	}

	renamed, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     position,
		},
		NewName: "ArtistId",
	})
	if err != nil {
		t.Fatal(err)
	}
	edits := renamed.Changes["file:///test.sql"]
	if len(edits) != 3 {
		t.Fatalf("Rename() edits = %#v, want declaration, GROUP BY, and consumer", edits)
	}
	for _, edit := range edits {
		if edit.NewText != "ArtistId" {
			t.Fatalf("Rename() edit = %#v, want ArtistId", edit)
		}
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
	if linked == nil || len(linked.Ranges) != 3 {
		t.Fatalf("LinkedEditingRange() = %#v, want three CTE-column ranges", linked)
	}
}

func TestRenameRejectsImplicitCTEColumn(t *testing.T) {
	const text = `WITH LocalRows AS (SELECT SingerId FROM Singers)
SELECT r.SingerId FROM LocalRows AS r`
	h := newParsedTestHandler(t, "/test.sql", text)
	position := newTextIndex(text).position(strings.LastIndex(text, "r.SingerId") + len("r."))

	prepared, err := h.PrepareRename(context.Background(), &protocol.PrepareRenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     position,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared != nil {
		t.Fatalf("PrepareRename() = %#v, want nil for implicit CTE column", prepared)
	}

	renamed, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     position,
		},
		NewName: "ArtistId",
	})
	if err != nil {
		t.Fatal(err)
	}
	if renamed != nil {
		t.Fatalf("Rename() = %#v, want nil for implicit CTE column", renamed)
	}
}

func TestRenameRejectsCTEColumnConflict(t *testing.T) {
	const text = `WITH LocalRows AS (
  SELECT SingerId AS Id, Name AS Label FROM Singers
)
SELECT r.Id FROM LocalRows AS r`
	h := newParsedTestHandler(t, "/test.sql", text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position: newTextIndex(text).position(
				strings.LastIndex(text, "r.Id") + len("r."),
			),
		},
		NewName: "Label",
	})
	if err == nil || got != nil {
		t.Fatalf("Rename() = %#v, %v; want CTE column conflict error", got, err)
	}
}
