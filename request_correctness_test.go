package main

import (
	"context"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestSelectionRangeReturnsOneHierarchyPerPosition(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT SingerId FROM Singers;\nSELECT AlbumId FROM Albums"
	h := newParsedTestHandler(t, path, text)
	positions := []protocol.Position{
		{Line: 0, Character: 8},
		{Line: 1, Character: 22},
	}

	got, err := h.SelectionRange(context.Background(), &protocol.SelectionRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Positions:    positions,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(positions) {
		t.Fatalf("SelectionRange() returned %d ranges, want %d", len(got), len(positions))
	}
	for i, selection := range got {
		if !rangeIncludesPosition(selection.Range, positions[i]) {
			t.Fatalf("SelectionRange()[%d] = %#v, does not include %#v", i, selection, positions[i])
		}
		for child, parent := &selection, selection.Parent; parent != nil; child, parent = parent, parent.Parent {
			if !rangeContains(parent.Range, child.Range) {
				t.Fatalf("SelectionRange()[%d] parent %#v does not contain child %#v", i, parent.Range, child.Range)
			}
		}
	}
}

func TestSelectionRangeHandlesEmptyPositions(t *testing.T) {
	h := newParsedTestHandler(t, "/test.sql", "SELECT 1")

	got, err := h.SelectionRange(context.Background(), &protocol.SelectionRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("SelectionRange() = %#v, want empty result", got)
	}
}

func TestSelectionRangeFallsBackWhenNoASTNodeContainsPosition(t *testing.T) {
	const path = "/test.sql"
	h := newParsedTestHandler(t, path, "")
	pos := protocol.Position{}

	got, err := h.SelectionRange(context.Background(), &protocol.SelectionRangeParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Positions:    []protocol.Position{pos},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Range.Start != pos || got[0].Range.End != pos || got[0].Parent != nil {
		t.Fatalf("SelectionRange() = %#v, want zero-width fallback at %#v", got, pos)
	}
}

func TestInlayHintRejectsZeroOrdinals(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT SingerId FROM Singers GROUP BY 0 ORDER BY 0"
	h := newParsedTestHandler(t, path, text)

	got, err := h.InlayHint(context.Background(), &protocol.InlayHintParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Range: protocol.Range{
			Start: protocol.Position{},
			End:   protocol.Position{Character: uint32(len(text))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("InlayHint() = %#v, want no hints for zero ordinals", got)
	}
}

func TestInlayHintHandlesExtraCompoundSelectItems(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT 1 UNION ALL SELECT 2, 3"
	h := newParsedTestHandler(t, path, text)

	_, err := h.InlayHint(context.Background(), &protocol.InlayHintParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Range: protocol.Range{
			Start: protocol.Position{},
			End:   protocol.Position{Character: uint32(len(text))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInlayHintFiltersToRequestedRange(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT 1 GROUP BY 1;\nSELECT 2 GROUP BY 1"
	h := newParsedTestHandler(t, path, text)

	got, err := h.InlayHint(context.Background(), &protocol.InlayHintParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Range: protocol.Range{
			Start: protocol.Position{},
			End:   protocol.Position{Line: 0, Character: 100},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Position.Line != 0 {
		t.Fatalf("InlayHint() = %#v, want only first-line hint", got)
	}
}
