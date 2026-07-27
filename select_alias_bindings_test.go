package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestSelectAliasNavigationAcrossClauses(t *testing.T) {
	const text = `SELECT SingerId AS singer, COUNT(*) AS total
FROM Singers
GROUP BY singer
HAVING total > 0
ORDER BY singer`
	h := newParsedTestHandler(t, "/test.sql", text)
	index := newTextIndex(text)
	singerDeclarationOffset := strings.Index(text, "singer")
	groupReferenceOffset := strings.Index(text, "singer\nHAVING")
	orderReferenceOffset := strings.LastIndex(text, "singer")
	totalDeclarationOffset := strings.Index(text, "total")
	totalReferenceOffset := strings.LastIndex(text, "total")

	definitions, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     index.position(groupReferenceOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantSingerDeclaration := index.rangeByByteOffsets(singerDeclarationOffset, singerDeclarationOffset+len("singer"))
	if len(definitions) != 1 || definitions[0].Range != wantSingerDeclaration {
		t.Fatalf("Definition(singer) = %#v, want declaration %#v", definitions, wantSingerDeclaration)
	}

	references, err := h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     index.position(orderReferenceOffset),
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 3 ||
		references[0].Range != wantSingerDeclaration ||
		references[1].Range.Start != index.position(groupReferenceOffset) ||
		references[2].Range.Start != index.position(orderReferenceOffset) {
		t.Fatalf("References(singer) = %#v, want declaration, GROUP BY, and ORDER BY", references)
	}

	totalDefinition, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     index.position(totalReferenceOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantTotalDeclaration := index.rangeByByteOffsets(totalDeclarationOffset, totalDeclarationOffset+len("total"))
	if len(totalDefinition) != 1 || totalDefinition[0].Range != wantTotalDeclaration {
		t.Fatalf("Definition(total) = %#v, want declaration %#v", totalDefinition, wantTotalDeclaration)
	}
}

func TestSelectAliasRenameAndLinkedEditing(t *testing.T) {
	const text = "SELECT SingerId AS singer FROM Singers GROUP BY singer ORDER BY singer"
	h := newParsedTestHandler(t, "/test.sql", text)
	position := newTextIndex(text).position(strings.LastIndex(text, "singer"))

	renamed, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     position,
		},
		NewName: "artist",
	})
	if err != nil {
		t.Fatal(err)
	}
	edits := renamed.Changes["file:///test.sql"]
	if len(edits) != 3 {
		t.Fatalf("Rename() edits = %#v, want declaration and two references", edits)
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
		t.Fatalf("LinkedEditingRange() = %#v, want declaration and two references", linked)
	}
}

func TestSelectAliasRenameRejectsDuplicateResultName(t *testing.T) {
	const text = "SELECT SingerId AS singer, Name AS title FROM Singers ORDER BY singer"
	h := newParsedTestHandler(t, "/test.sql", text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(strings.LastIndex(text, "singer")),
		},
		NewName: "title",
	})
	if err == nil || got != nil {
		t.Fatalf("Rename() = %#v, %v, want duplicate SELECT alias error", got, err)
	}
}

func TestSelectAliasOverridesFromColumnName(t *testing.T) {
	const text = "SELECT SingerId AS SingerId FROM Singers GROUP BY SingerId"
	h := newParsedTestHandler(t, "/test.sql", text)
	referenceOffset := strings.LastIndex(text, "SingerId")

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(referenceOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	declarationOffset := strings.Index(text, "AS SingerId") + len("AS ")
	want := newTextIndex(text).rangeByByteOffsets(declarationOffset, declarationOffset+len("SingerId"))
	if len(got) != 1 || got[0].Range != want {
		t.Fatalf("Definition() = %#v, want SELECT alias override %#v", got, want)
	}
}

func TestSelectAliasBindingDoesNotCrossNestedQuery(t *testing.T) {
	const text = `SELECT SingerId AS value,
  (SELECT AlbumId AS value FROM Albums ORDER BY value)
FROM Singers
ORDER BY value`
	h := newParsedTestHandler(t, "/test.sql", text)
	albumOffset := strings.Index(text, "AlbumId")
	innerReferenceOffset := strings.Index(text, "value)")
	outerReferenceOffset := strings.LastIndex(text, "value")

	tests := []struct {
		name              string
		referenceOffset   int
		declarationOffset int
	}{
		{
			name:              "inner",
			referenceOffset:   innerReferenceOffset,
			declarationOffset: strings.Index(text[albumOffset:], "value") + albumOffset,
		},
		{
			name:              "outer",
			referenceOffset:   outerReferenceOffset,
			declarationOffset: strings.Index(text, "value"),
		},
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
			want := newTextIndex(text).rangeByByteOffsets(test.declarationOffset, test.declarationOffset+len("value"))
			if len(got) != 1 || got[0].Range != want {
				t.Fatalf("Definition() = %#v, want scoped declaration %#v", got, want)
			}
		})
	}
}

func TestDuplicateSelectAliasesFailClosed(t *testing.T) {
	const text = "SELECT SingerId AS value, Name AS value FROM Singers ORDER BY value"
	h := newParsedTestHandler(t, "/test.sql", text)

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(strings.LastIndex(text, "value")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Definition() = %#v, want no result for duplicate SELECT aliases", got)
	}
}

func TestSelectAliasPathReference(t *testing.T) {
	const text = "SELECT STRUCT(SingerId AS id) AS result FROM Singers ORDER BY result.id"
	h := newParsedTestHandler(t, "/test.sql", text)
	referenceOffset := strings.LastIndex(text, "result")

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(referenceOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	declarationOffset := strings.Index(text, "result")
	want := newTextIndex(text).rangeByByteOffsets(declarationOffset, declarationOffset+len("result"))
	if len(got) != 1 || got[0].Range != want {
		t.Fatalf("Definition() = %#v, want SELECT alias declaration %#v", got, want)
	}
}

func TestSelectAndTableAliasPathAmbiguityFailsClosed(t *testing.T) {
	const text = "SELECT s AS s FROM Singers AS s GROUP BY s.Missing"
	h := newParsedTestHandler(t, "/test.sql", text)
	addParsedTestDocument(t, h, "/schema.sql", "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)")
	qualifierOffset := strings.LastIndex(text, "s.Missing")
	memberOffset := qualifierOffset + len("s.")

	for _, offset := range []int{qualifierOffset, memberOffset} {
		got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
				Position:     newTextIndex(text).position(offset),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("Definition(offset %d) = %#v, want no result for ambiguous alias path", offset, got)
		}
	}

	prepared, err := h.PrepareRename(context.Background(), &protocol.PrepareRenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(qualifierOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared != nil {
		t.Fatalf("PrepareRename() = %#v, want nil for ambiguous alias path", prepared)
	}

	completions, err := h.Completion(context.Background(), &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(memberOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completions.Items) != 0 {
		t.Fatalf("Completion() items = %#v, want none for ambiguous alias path", completions.Items)
	}

	diagnostics, err := h.Diagnostic(context.Background(), &protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	full := diagnostics.Value.(protocol.FullDocumentDiagnosticReport)
	if len(full.Items) != 0 {
		t.Fatalf("Diagnostic() items = %#v, want none for ambiguous alias path", full.Items)
	}
}
