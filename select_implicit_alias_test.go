package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestImplicitSelectAliasNavigation(t *testing.T) {
	const text = "SELECT SingerId FROM Singers GROUP BY SingerId ORDER BY SingerId"
	h := newParsedTestHandler(t, "/test.sql", text)
	index := newTextIndex(text)
	declarationOffset := strings.Index(text, "SingerId")
	groupOffset := strings.Index(text[declarationOffset+1:], "SingerId") + declarationOffset + 1
	orderOffset := strings.LastIndex(text, "SingerId")

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     index.position(orderOffset),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDeclaration := index.rangeByByteOffsets(declarationOffset, declarationOffset+len("SingerId"))
	if len(got) != 1 || got[0].Range != wantDeclaration {
		t.Fatalf("Definition() = %#v, want implicit alias declaration %#v", got, wantDeclaration)
	}

	references, err := h.References(context.Background(), &protocol.ReferenceParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     index.position(groupOffset),
		},
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(references) != 3 ||
		references[0].Range != wantDeclaration ||
		references[1].Range.Start != index.position(groupOffset) ||
		references[2].Range.Start != index.position(orderOffset) {
		t.Fatalf("References() = %#v, want implicit declaration and two references", references)
	}
}

func TestImplicitPathAndSelectorAliases(t *testing.T) {
	const text = `SELECT s.SingerId, (STRUCT(1 AS field)).field
FROM Singers AS s
ORDER BY SingerId, field`
	h := newParsedTestHandler(t, "/test.sql", text)

	tests := []struct {
		name        string
		reference   int
		declaration int
	}{
		{
			name:        "path",
			reference:   strings.LastIndex(text, "SingerId"),
			declaration: strings.Index(text, "SingerId"),
		},
		{
			name:        "selector",
			reference:   strings.LastIndex(text, "field"),
			declaration: strings.Index(text, "field\n"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
					Position:     newTextIndex(text).position(test.reference),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := newTextIndex(text).rangeByByteOffsets(test.declaration, test.declaration+len("field"))
			if test.name == "path" {
				want = newTextIndex(text).rangeByByteOffsets(test.declaration, test.declaration+len("SingerId"))
			}
			if len(got) != 1 || got[0].Range != want {
				t.Fatalf("Definition() = %#v, want implicit alias declaration %#v", got, want)
			}
		})
	}
}

func TestImplicitSelectAliasCannotBeRenamed(t *testing.T) {
	const text = "SELECT SingerId FROM Singers ORDER BY SingerId"
	h := newParsedTestHandler(t, "/test.sql", text)
	position := newTextIndex(text).position(strings.LastIndex(text, "SingerId"))

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
		t.Fatalf("PrepareRename() = %#v, want nil for implicit alias", prepared)
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
		t.Fatalf("Rename() = %#v, want nil for implicit alias", renamed)
	}
}

func TestDuplicateImplicitSelectAliasesFailClosed(t *testing.T) {
	const text = "SELECT s.Id, a.Id FROM Singers AS s JOIN Albums AS a ON TRUE ORDER BY Id"
	h := newParsedTestHandler(t, "/test.sql", text)

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     newTextIndex(text).position(strings.LastIndex(text, "Id")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Definition() = %#v, want no result for duplicate implicit aliases", got)
	}
}
