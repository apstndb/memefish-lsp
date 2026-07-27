package main

import (
	"context"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/token"
)

func TestTextIndexConvertsUTF8OffsetsAndUTF16Positions(t *testing.T) {
	const text = "a😀é\n日本"
	index := newTextIndex(text)
	tests := []struct {
		byteOffset int
		position   protocol.Position
	}{
		{byteOffset: 0, position: protocol.Position{}},
		{byteOffset: 1, position: protocol.Position{Character: 1}},
		{byteOffset: 5, position: protocol.Position{Character: 3}},
		{byteOffset: 7, position: protocol.Position{Character: 4}},
		{byteOffset: 8, position: protocol.Position{Line: 1}},
		{byteOffset: 11, position: protocol.Position{Line: 1, Character: 1}},
		{byteOffset: 14, position: protocol.Position{Line: 1, Character: 2}},
	}
	for _, test := range tests {
		if got := index.position(test.byteOffset); got != test.position {
			t.Errorf("position(%d) = %#v, want %#v", test.byteOffset, got, test.position)
		}
		got, ok := index.byteOffset(test.position)
		if !ok || got != test.byteOffset {
			t.Errorf("byteOffset(%#v) = %d, %t; want %d, true", test.position, got, ok, test.byteOffset)
		}
	}
}

func TestTextIndexRejectsPositionInsideSurrogatePair(t *testing.T) {
	index := newTextIndex("a😀")

	got, ok := index.byteOffset(protocol.Position{Character: 2})
	if ok || got != 1 {
		t.Fatalf("byteOffset(inside surrogate pair) = %d, %t; want 1, false", got, ok)
	}
}

func TestTextIndexSingleLineUTF16Range(t *testing.T) {
	index := newTextIndex("a😀\né")

	line, character, length, ok := index.singleLineUTF16Range(1, 5)
	if !ok || line != 0 || character != 1 || length != 2 {
		t.Fatalf("singleLineUTF16Range() = %d, %d, %d, %t; want 0, 1, 2, true", line, character, length, ok)
	}
	if _, _, _, ok := index.singleLineUTF16Range(1, 8); ok {
		t.Fatal("singleLineUTF16Range() accepted a multiline range")
	}
}

func TestCompletionPrefixAtUsesUTF16Position(t *testing.T) {
	const text = "SELECT '😀', Si"

	if got := completionPrefixAt(text, protocol.Position{Character: 15}); got != "Si" {
		t.Fatalf("completionPrefixAt() = %q, want Si", got)
	}
}

func TestDefinitionUsesUTF16PositionAfterAstralRune(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT '😀', * FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 1, Character: 21},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Range.Start != (protocol.Position{Character: 13}) {
		t.Fatalf("Definition() = %#v, want Singers declaration at character 13", got)
	}
}

func TestIdentifierRangeUsesUTF16AfterAstralRune(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT '😀', * FROM Singers"

	name, got, ok := identifierAtPositionWithRange(path, text, protocol.Position{Character: 21})
	want := protocol.Range{
		Start: protocol.Position{Character: 20},
		End:   protocol.Position{Character: 27},
	}
	if !ok || name != "Singers" || got != want {
		t.Fatalf("identifierAtPositionWithRange() = %q, %#v, %t; want Singers, %#v, true", name, got, ok, want)
	}
}

func TestSemanticTokenUsesUTF16Length(t *testing.T) {
	lex := newLexer("/test.sql", "😀 1")

	got := newSemanticToken(lex, token.Pos(5), token.Pos(6), protocol.NumberType)
	if !got.valid || got.Line != 0 || got.Col != 3 || got.Length != 1 {
		t.Fatalf("newSemanticToken() = %#v, want UTF-16 column 3 and length 1", got)
	}
}

func TestSemanticTokenRejectsMultilineRangeUntilSplitting(t *testing.T) {
	lex := newLexer("/test.sql", "a\nb")

	got := newSemanticToken(lex, token.Pos(0), token.Pos(3), protocol.CommentType)
	if got.valid {
		t.Fatalf("newSemanticToken() = %#v, want invalid multiline token", got)
	}
}

func TestDiagnosticsUseUTF16Ranges(t *testing.T) {
	const text = "😀 ?"
	err := memefish.MultiError{{
		Message: "unexpected token",
		Position: &token.Position{
			Pos: token.Pos(5),
			End: token.Pos(6),
		},
	}}

	got := diagnosticsFromParseError(err, text)
	want := protocol.Range{
		Start: protocol.Position{Character: 3},
		End:   protocol.Position{Character: 4},
	}
	if len(got) != 1 || got[0].Range != want {
		t.Fatalf("diagnosticsFromParseError() = %#v, want range %#v", got, want)
	}
}
