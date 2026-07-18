package main

import (
	"context"
	"log/slog"
	"slices"
	"strings"
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

func TestSignatureHelpTracksActiveParameter(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT IF(TRUE, 1, "
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.SignatureHelp(context.Background(), &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Character: uint32(len(text))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Signatures) != 1 || got.Signatures[0].Label != "IF(expr, true_result, else_result)" {
		t.Fatalf("SignatureHelp() = %#v, want IF signature", got)
	}
	if got.ActiveParameter == nil || *got.ActiveParameter != 2 {
		t.Fatalf("SignatureHelp() active parameter = %#v, want 2", got.ActiveParameter)
	}
}

func TestSignatureHelpUsesInnermostFunctionCall(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT IF(TRUE, SUBSTR(name, "
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.SignatureHelp(context.Background(), &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Character: uint32(len(text))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Signatures[0].Label != "SUBSTR(value, position[, length])" {
		t.Fatalf("SignatureHelp() = %#v, want nested SUBSTR signature", got)
	}
	if got.ActiveParameter == nil || *got.ActiveParameter != 1 {
		t.Fatalf("SignatureHelp() active parameter = %#v, want 1", got.ActiveParameter)
	}
}

func TestSignatureHelpIgnoresUnknownFunction(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT CUSTOM_FUNCTION("
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.SignatureHelp(context.Background(), &protocol.SignatureHelpParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Character: uint32(len(text))},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("SignatureHelp() = %#v, want nil for unknown function", got)
	}
}

func TestCompletionPrefixAt(t *testing.T) {
	got := completionPrefixAt("SELECT singer_id", protocol.Position{Line: 0, Character: 9})
	if got != "si" {
		t.Fatalf("completionPrefixAt() = %q, want %q", got, "si")
	}
}

func TestCompletionItemsIncludesSignatureCatalogFunctions(t *testing.T) {
	items := completionItems("SELECT ", "sub")
	if len(items) != 2 {
		t.Fatalf("completionItems() returned %d SUB functions, want SUBSTR and SUBSTRING: %#v", len(items), items)
	}
	if items[0].Label != "SUBSTR" || items[0].Kind != protocol.FunctionCompletion {
		t.Fatalf("completionItems()[0] = %#v, want SUBSTR function", items[0])
	}
}

func TestResolveCompletionItemAddsFunctionDocumentation(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	original := &protocol.CompletionItem{Label: "SUBSTR", Kind: protocol.FunctionCompletion}

	got, err := h.ResolveCompletionItem(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	if got.Detail != "SUBSTR(value, position[, length])" {
		t.Fatalf("ResolveCompletionItem() detail = %q", got.Detail)
	}
	if got.Documentation == nil {
		t.Fatal("ResolveCompletionItem() documentation is nil")
	}
	markup, ok := got.Documentation.Value.(protocol.MarkupContent)
	if !ok || !strings.Contains(markup.Value, "substring") {
		t.Fatalf("ResolveCompletionItem() documentation = %#v", got.Documentation.Value)
	}
	if original.Detail != "" || original.Documentation != nil {
		t.Fatalf("ResolveCompletionItem() mutated original item: %#v", original)
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

func TestDidCloseClearsDiagnosticsAndDocumentState(t *testing.T) {
	const path = "/test.sql"
	h := newParsedTestHandler(t, path, "SELECT 1")
	client := &recordingClient{}
	h.SetClient(client)

	err := h.DidClose(context.Background(), &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.fileToContentMap[path]; ok {
		t.Fatal("DidClose() retained document content")
	}
	if _, ok := h.parsedMap[path]; ok {
		t.Fatal("DidClose() retained parsed document")
	}
	if len(client.diagnostics) != 1 || len(client.diagnostics[0].Diagnostics) != 0 {
		t.Fatalf("published diagnostics = %#v, want one empty update", client.diagnostics)
	}
}

func TestDiagnosticReturnsFullAndUnchangedReports(t *testing.T) {
	const path = "/test.sql"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte("SELECT FROM")

	got, err := h.Diagnostic(context.Background(), &protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	full, ok := got.Value.(protocol.FullDocumentDiagnosticReport)
	if !ok {
		t.Fatalf("Diagnostic() report = %#v, want full report", got.Value)
	}
	if full.ResultID == "" || len(full.Items) == 0 {
		t.Fatalf("Diagnostic() full report = %#v, want result ID and parse diagnostic", full)
	}

	got, err = h.Diagnostic(context.Background(), &protocol.DocumentDiagnosticParams{
		TextDocument:     protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		PreviousResultID: full.ResultID,
	})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, ok := got.Value.(protocol.UnchangedDocumentDiagnosticReport)
	if !ok || unchanged.ResultID != full.ResultID {
		t.Fatalf("Diagnostic() report = %#v, want unchanged report", got.Value)
	}
}

func TestFormattingCanonicalizesCommentFreeDocument(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT  1;SELECT 2"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.Formatting(context.Background(), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("Formatting() returned %d edits, want 1: %#v", len(got), got)
	}
	if got[0].NewText != "SELECT 1;\nSELECT 2;\n" {
		t.Fatalf("Formatting() text = %q", got[0].NewText)
	}
	if got[0].Range.End.Line != 0 || got[0].Range.End.Character != uint32(len(text)) {
		t.Fatalf("Formatting() range = %#v, want whole document", got[0].Range)
	}
}

func TestFormattingPreservesCommentedDocument(t *testing.T) {
	const path = "/test.sql"
	const text = "-- keep this comment\nSELECT  1"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.Formatting(context.Background(), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Formatting() returned edits for commented document: %#v", got)
	}
}

func TestFormattingRejectsInvalidDocument(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT FROM"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.Formatting(context.Background(), &protocol.DocumentFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Formatting() returned edits for invalid document: %#v", got)
	}
}

func TestRangeFormattingFormatsFullySelectedStatement(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT  1;\nSELECT  2"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.RangeFormatting(context.Background(), &protocol.DocumentRangeFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Range: protocol.Range{
			Start: protocol.Position{Line: 1},
			End:   protocol.Position{Line: 1, Character: 9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("RangeFormatting() returned %d edits, want 1: %#v", len(got), got)
	}
	if got[0].NewText != "SELECT 2" || got[0].Range.Start.Line != 1 {
		t.Fatalf("RangeFormatting() edit = %#v, want formatted second statement", got[0])
	}
}

func TestRangeFormattingRejectsPartialStatement(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT  1"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.RangeFormatting(context.Background(), &protocol.DocumentRangeFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 1},
			End:   protocol.Position{Line: 0, Character: 9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("RangeFormatting() returned edits for partial statement: %#v", got)
	}
}

func TestRangeFormattingPreservesCommentsInSelection(t *testing.T) {
	const path = "/test.sql"
	const text = "-- keep this comment\nSELECT  1"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.RangeFormatting(context.Background(), &protocol.DocumentRangeFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Range: protocol.Range{
			Start: protocol.Position{},
			End:   protocol.Position{Line: 1, Character: 9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("RangeFormatting() returned edits for commented selection: %#v", got)
	}
}

func TestRangesFormattingFormatsMultipleStatements(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT  1;\nSELECT  2"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.RangesFormatting(context.Background(), &protocol.DocumentRangesFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Ranges: []protocol.Range{
			{Start: protocol.Position{}, End: protocol.Position{Character: 9}},
			{Start: protocol.Position{Line: 1}, End: protocol.Position{Line: 1, Character: 9}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].NewText != "SELECT 1" || got[1].NewText != "SELECT 2" {
		t.Fatalf("RangesFormatting() = %#v, want two formatted statements", got)
	}
}

func TestOnTypeFormattingFormatsStatementBeforeSemicolon(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT  1;"
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap[path] = []byte(text)

	got, err := h.OnTypeFormatting(context.Background(), &protocol.DocumentOnTypeFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Position:     protocol.Position{Character: uint32(len(text))},
		Ch:           ";",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].NewText != "SELECT 1" {
		t.Fatalf("OnTypeFormatting() = %#v, want formatted statement", got)
	}
	if got[0].Range.End.Character != uint32(len(text)-1) {
		t.Fatalf("OnTypeFormatting() range = %#v, want semicolon excluded", got[0].Range)
	}
}

func TestOnTypeFormattingIgnoresOtherCharacters(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	h.fileToContentMap["/test.sql"] = []byte("SELECT  1")

	got, err := h.OnTypeFormatting(context.Background(), &protocol.DocumentOnTypeFormattingParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Position:     protocol.Position{Character: 9},
		Ch:           " ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("OnTypeFormatting() returned edits for non-trigger character: %#v", got)
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

func TestFilterSemanticTokensReencodesSelectedRange(t *testing.T) {
	full := &protocol.SemanticTokens{Data: []uint32{
		0, 1, 2, 1, 0,
		1, 3, 4, 2, 1,
		0, 6, 2, 3, 0,
	}}

	got := filterSemanticTokens(full, protocol.Range{
		Start: protocol.Position{Line: 1, Character: 2},
		End:   protocol.Position{Line: 1, Character: 8},
	})
	want := []uint32{1, 3, 4, 2, 1}
	if !slices.Equal(got.Data, want) {
		t.Fatalf("filterSemanticTokens() = %v, want %v", got.Data, want)
	}
}

func TestSemanticTokensFullDeltaReturnsCurrentFullTokens(t *testing.T) {
	h := newParsedTestHandler(t, "/test.sql", "SELECT 1")
	h.tokenTypeMap = map[protocol.SemanticTokenTypes]uint32{
		protocol.KeywordType: 0,
		protocol.NumberType:  1,
	}
	h.tokenModifierMap = map[protocol.SemanticTokenModifiers]uint32{}

	got, err := h.SemanticTokensFullDelta(context.Background(), &protocol.SemanticTokensDeltaParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	full, ok := got.(*protocol.SemanticTokens)
	if !ok || full.ResultID == "" || len(full.Data) == 0 {
		t.Fatalf("SemanticTokensFullDelta() = %#v, want full tokens with result ID", got)
	}
}

func TestCodeActionReturnsInsertASQuickFix(t *testing.T) {
	const path = "/test.sql"
	const text = "SELECT SingerId singer FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.CodeAction(context.Background(), &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Range: protocol.Range{
			Start: protocol.Position{Character: 16},
			End:   protocol.Position{Character: 16},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("CodeAction() returned %d actions, want 1: %#v", len(got), got)
	}
	if got[0].Title != "Insert AS keyword" || got[0].Kind != protocol.QuickFix || !got[0].IsPreferred {
		t.Fatalf("CodeAction() = %#v, want preferred AS quick fix", got[0])
	}
	edits := got[0].Edit.Changes["file:///test.sql"]
	if len(edits) != 1 || edits[0].NewText != "AS " {
		t.Fatalf("CodeAction() edits = %#v, want AS insertion", edits)
	}
}

func TestCodeActionHonorsRequestedKinds(t *testing.T) {
	h := newParsedTestHandler(t, "/test.sql", "SELECT SingerId singer FROM Singers")

	got, err := h.CodeAction(context.Background(), &protocol.CodeActionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
		Range: protocol.Range{
			Start: protocol.Position{Character: 16},
			End:   protocol.Position{Character: 16},
		},
		Context: protocol.CodeActionContext{Only: []protocol.CodeActionKind{protocol.Source}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("CodeAction() returned actions for source-only request: %#v", got)
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

func TestDeclarationReturnsLocalCreateTableLocation(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT * FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.Declaration(context.Background(), &protocol.DeclarationParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentURI("file://" + path)},
			Position:     protocol.Position{Line: 1, Character: 16},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	locations, ok := got.Value.(protocol.Declaration)
	if !ok || len(locations) != 1 {
		t.Fatalf("Declaration() = %#v, want one declaration location", got.Value)
	}
	if locations[0].Range.Start.Line != 0 {
		t.Fatalf("Declaration() range = %#v, want line 0", locations[0].Range)
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

func TestTypeDefinitionReturnsUniqueColumnSchemaType(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	addParsedTestDocument(t, h, "/schema.sql", "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)")
	addParsedTestDocument(t, h, "/query.sql", "SELECT SingerId FROM Singers")

	got, err := h.TypeDefinition(context.Background(), &protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     protocol.Position{Line: 0, Character: 9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("TypeDefinition() returned %d locations, want 1: %#v", len(got), got)
	}
	if got[0].URI != "file:///schema.sql" {
		t.Fatalf("TypeDefinition() URI = %q, want schema document", got[0].URI)
	}
	if got[0].Range.Start.Line != 0 || got[0].Range.Start.Character != 31 {
		t.Fatalf("TypeDefinition() range = %#v, want INT64 type", got[0].Range)
	}
}

func TestTypeDefinitionRejectsAmbiguousColumnName(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	addParsedTestDocument(t, h, "/schema.sql", "CREATE TABLE Singers (Id INT64) PRIMARY KEY (Id); CREATE TABLE Albums (Id INT64) PRIMARY KEY (Id)")
	addParsedTestDocument(t, h, "/query.sql", "SELECT Id FROM Singers")

	got, err := h.TypeDefinition(context.Background(), &protocol.TypeDefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     protocol.Position{Line: 0, Character: 8},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("TypeDefinition() returned ambiguous locations: %#v", got)
	}
}

func TestHoverDescribesLocalTable(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	addParsedTestDocument(t, h, "/schema.sql", "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)")
	addParsedTestDocument(t, h, "/query.sql", "SELECT SingerId FROM Singers")

	got, err := h.Hover(context.Background(), &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     protocol.Position{Line: 0, Character: 23},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !strings.Contains(got.Contents.Value, "CREATE TABLE Singers") {
		t.Fatalf("Hover() = %#v, want table DDL", got)
	}
	if got.Range.Start.Character != 21 || got.Range.End.Character != 28 {
		t.Fatalf("Hover() range = %#v, want table reference", got.Range)
	}
}

func TestHoverDescribesUniqueColumn(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	addParsedTestDocument(t, h, "/schema.sql", "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)")
	addParsedTestDocument(t, h, "/query.sql", "SELECT SingerId FROM Singers")

	got, err := h.Hover(context.Background(), &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     protocol.Position{Line: 0, Character: 9},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !strings.Contains(got.Contents.Value, "SingerId INT64") {
		t.Fatalf("Hover() = %#v, want column definition", got)
	}
	if got.Range.Start.Character != 7 || got.Range.End.Character != 15 {
		t.Fatalf("Hover() range = %#v, want column reference", got.Range)
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

func TestLinkedEditingRangeReturnsMatchingLocalTableRanges(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT * FROM Singers"
	h := newParsedTestHandler(t, path, text)

	got, err := h.LinkedEditingRange(context.Background(), &protocol.LinkedEditingRangeParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 1, Character: 16},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Ranges) != 2 {
		t.Fatalf("LinkedEditingRange() = %#v, want declaration and reference", got)
	}
	if got.Ranges[0].Start.Line != 0 || got.Ranges[1].Start.Line != 1 {
		t.Fatalf("LinkedEditingRange() ranges = %#v", got.Ranges)
	}
	if got.WordPattern == "" {
		t.Fatal("LinkedEditingRange() word pattern is empty")
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
