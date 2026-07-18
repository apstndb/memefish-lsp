package main

import (
	"context"
	"log/slog"
	"slices"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish"
)

type recordingClient struct {
	protocol.Client
	diagnostics []*protocol.PublishDiagnosticsParams
}

func (c *recordingClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	c.diagnostics = append(c.diagnostics, params)
	return nil
}

func TestCompletionItemsIncludesGoogleSQLKeywordsAndDocumentIdentifiers(t *testing.T) {
	keywordItems := completionItems("SELECT singer_id FROM Singers", "se")
	keywordLabels := make([]string, 0, len(keywordItems))
	for _, item := range keywordItems {
		keywordLabels = append(keywordLabels, item.Label)
	}
	if !slices.Contains(keywordLabels, "SELECT") {
		t.Fatalf("completion labels = %v, want %q", keywordLabels, "SELECT")
	}

	items := completionItems("SELECT singer_id FROM Singers", "si")

	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}

	for _, want := range []string{"singer_id", "Singers"} {
		if !slices.Contains(labels, want) {
			t.Fatalf("completion labels = %v, want %q", labels, want)
		}
	}
}

func TestCompletionPrefixAt(t *testing.T) {
	got := completionPrefixAt("SELECT singer_id", protocol.Position{Line: 0, Character: 9})
	if got != "si" {
		t.Fatalf("completionPrefixAt() = %q, want %q", got, "si")
	}
}

func TestDidSaveReparsesIncludedText(t *testing.T) {
	const path = "/test.sql"
	text := "SELECT 1"
	h := NewHandler(slog.Default(), nil)
	client := &recordingClient{}
	h.SetClient(client)

	err := h.DidSave(context.Background(), &protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI("file://" + path)},
		Text:         &text,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(h.fileToContentMap[path]); got != text {
		t.Fatalf("saved text = %q, want %q", got, text)
	}
	if len(h.parsedMap[path]) != 1 {
		t.Fatalf("parsed statements = %d, want 1", len(h.parsedMap[path]))
	}
	if len(client.diagnostics) != 1 || len(client.diagnostics[0].Diagnostics) != 0 {
		t.Fatalf("published diagnostics = %#v, want one empty update", client.diagnostics)
	}
}

func TestDocumentHighlightHighlightsIdentifierOccurrences(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT singer_id FROM Singers WHERE singer_id = 1"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.DocumentHighlight(context.Background(), &protocol.DocumentHighlightParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI("file://" + path)},
			Position:     protocol.Position{Line: 0, Character: 9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("DocumentHighlight() returned %d ranges, want 2: %#v", len(got), got)
	}
}

func TestDefinitionReturnsLocalCreateTableLocation(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT * FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI("file://" + path)},
			Position:     protocol.Position{Line: 1, Character: 16},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Definition() returned %d locations, want 1: %#v", len(got), got)
	}
	if got[0].Range.Start.Line != 0 {
		t.Fatalf("Definition() range = %#v, want line 0", got[0].Range)
	}
}

func TestImplementationReturnsLocalCreateTableLocation(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT * FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.Implementation(context.Background(), &protocol.ImplementationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI("file://" + path)},
			Position:     protocol.Position{Line: 1, Character: 16},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Implementation() returned %d locations, want 1: %#v", len(got), got)
	}
	if got[0].Range.Start.Line != 0 {
		t.Fatalf("Implementation() range = %#v, want line 0", got[0].Range)
	}
}

func TestSymbolReturnsFuzzyMatchedSchemaSymbolsAcrossOpenDocuments(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	addParsedTestDocument(t, h, "/singers.sql", "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)")
	addParsedTestDocument(t, h, "/albums.sql", "CREATE TABLE Albums (AlbumId INT64) PRIMARY KEY (AlbumId)")

	got, err := h.Symbol(context.Background(), &protocol.WorkspaceSymbolParams{Query: "alb"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Symbol() returned %d symbols, want table and column: %#v", len(got), got)
	}
	if got[0].Name != "AlbumId" || got[1].Name != "Albums" {
		t.Fatalf("Symbol() names = %q, %q, want AlbumId and Albums", got[0].Name, got[1].Name)
	}
	for _, symbol := range got {
		if symbol.Location.URI != "file:///albums.sql" {
			t.Fatalf("Symbol() URI = %q, want albums document", symbol.Location.URI)
		}
	}
}

func TestReferencesReturnsLocalTableReferences(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT * FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.References(context.Background(), &protocol.ReferenceParams{
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI("file://" + path)},
			Position:     protocol.Position{Line: 1, Character: 16},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("References() returned %d locations, want declaration and use: %#v", len(got), got)
	}
}

func TestPrepareRenameReturnsLocalTableName(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT * FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.PrepareRename(context.Background(), &protocol.PrepareRenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI("file://" + path)},
			Position:     protocol.Position{Line: 1, Character: 16},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("PrepareRename() returned nil, want table range")
	}
	if got.Placeholder != "Singers" {
		t.Fatalf("PrepareRename() placeholder = %q, want %q", got.Placeholder, "Singers")
	}
	if got.Range.Start.Line != 1 || got.Range.Start.Character != 14 || got.Range.End.Character != 21 {
		t.Fatalf("PrepareRename() range = %#v, want table reference range", got.Range)
	}
}

func TestRenameReturnsWorkspaceEditForLocalTableReferences(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT * FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		NewName: "Artists",
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI("file://" + path)},
			Position:     protocol.Position{Line: 1, Character: 16},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("Rename() returned nil, want workspace edit")
	}
	edits := got.Changes[protocol.DocumentURI("file://"+path)]
	if len(edits) != 2 {
		t.Fatalf("Rename() returned %d edits, want declaration and use: %#v", len(edits), edits)
	}
	for _, edit := range edits {
		if edit.NewText != "Artists" {
			t.Fatalf("Rename() edit NewText = %q, want %q", edit.NewText, "Artists")
		}
	}
	if edits[0].Range.Start.Line != 0 || edits[0].Range.Start.Character != 13 || edits[0].Range.End.Character != 20 {
		t.Fatalf("Rename() declaration edit range = %#v, want CREATE TABLE name", edits[0].Range)
	}
	if edits[1].Range.Start.Line != 1 || edits[1].Range.Start.Character != 14 || edits[1].Range.End.Character != 21 {
		t.Fatalf("Rename() reference edit range = %#v, want SELECT table reference", edits[1].Range)
	}
}

func newParsedTestHandler(t *testing.T, path, text string) *Handler {
	t.Helper()
	h := NewHandler(slog.Default(), nil)
	addParsedTestDocument(t, h, path, text)
	return h
}

func addParsedTestDocument(t *testing.T, h *Handler, path, text string) {
	t.Helper()

	parsed, err := memefish.ParseStatements(path, text)
	if err != nil {
		t.Fatal(err)
	}

	h.fileToContentMap[path] = []byte(text)
	h.parsedMap[path] = parsed
}
