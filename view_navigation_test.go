package main

import (
	"context"
	"strings"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

const activeSingersViewDDL = `CREATE VIEW ActiveSingers SQL SECURITY INVOKER AS
SELECT * FROM Singers`

func TestDefinitionReturnsWorkspaceCreateViewLocation(t *testing.T) {
	const query = "SELECT * FROM ActiveSingers"
	h := newParsedTestHandler(t, "/query.sql", query)
	addParsedTestDocument(t, h, "/schema.sql", activeSingersViewDDL)

	got, err := h.Definition(context.Background(), &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     newTextIndex(query).position(strings.Index(query, "ActiveSingers")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].URI != "file:///schema.sql" {
		t.Fatalf("Definition() = %#v, want schema view location", got)
	}
	want := newTextIndex(activeSingersViewDDL).rangeByByteOffsets(
		strings.Index(activeSingersViewDDL, "ActiveSingers"),
		strings.Index(activeSingersViewDDL, "ActiveSingers")+len("ActiveSingers"),
	)
	if got[0].Range != want {
		t.Fatalf("Definition() range = %#v, want %#v", got[0].Range, want)
	}
}

func TestReferencesReturnsWorkspaceViewReferences(t *testing.T) {
	const query = "SELECT * FROM ActiveSingers"
	h := newParsedTestHandler(t, "/query.sql", query)
	addParsedTestDocument(t, h, "/schema.sql", activeSingersViewDDL)
	addParsedTestDocument(t, h, "/dependent.sql", `CREATE VIEW FeaturedSingers SQL SECURITY INVOKER AS
SELECT * FROM ActiveSingers`)
	addParsedTestDocument(t, h, "/shadowed.sql", `WITH ActiveSingers AS (SELECT 1)
SELECT * FROM ActiveSingers`)

	got, err := h.References(context.Background(), &protocol.ReferenceParams{
		Context: protocol.ReferenceContext{IncludeDeclaration: true},
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     newTextIndex(query).position(strings.Index(query, "ActiveSingers")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("References() = %#v, want declaration and two view references", got)
	}
	wantURIs := []protocol.DocumentURI{"file:///dependent.sql", "file:///query.sql", "file:///schema.sql"}
	for i, want := range wantURIs {
		if got[i].URI != want {
			t.Fatalf("References()[%d].URI = %q, want %q", i, got[i].URI, want)
		}
	}
}

func TestHoverDescribesLocalView(t *testing.T) {
	const query = "SELECT * FROM ActiveSingers"
	h := newParsedTestHandler(t, "/query.sql", query)
	addParsedTestDocument(t, h, "/schema.sql", activeSingersViewDDL)

	got, err := h.Hover(context.Background(), &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     newTextIndex(query).position(strings.Index(query, "ActiveSingers")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !strings.Contains(got.Contents.Value, "**View** `ActiveSingers`") ||
		!strings.Contains(got.Contents.Value, "CREATE VIEW ActiveSingers") {
		t.Fatalf("Hover() = %#v, want view DDL", got)
	}
	want := newTextIndex(query).rangeByByteOffsets(
		strings.Index(query, "ActiveSingers"),
		strings.Index(query, "ActiveSingers")+len("ActiveSingers"),
	)
	if got.Range != want {
		t.Fatalf("Hover() range = %#v, want %#v", got.Range, want)
	}
}

func TestCodeLensCountsWorkspaceViewReferences(t *testing.T) {
	h := newParsedTestHandler(t, "/schema.sql", activeSingersViewDDL)
	addParsedTestDocument(t, h, "/query.sql", "SELECT * FROM ActiveSingers")
	addParsedTestDocument(t, h, "/second_query.sql", "SELECT SingerId FROM ActiveSingers")

	lenses, err := h.CodeLens(context.Background(), &protocol.CodeLensParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: "file:///schema.sql"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lenses) != 1 || lenses[0].Command == nil || lenses[0].Command.Title != "2 references" {
		t.Fatalf("CodeLens() = %#v, want two workspace view references", lenses)
	}
}

func TestCompletionUsesKnownViewColumns(t *testing.T) {
	const view = `CREATE VIEW ActiveSingers SQL SECURITY INVOKER AS
SELECT SingerId AS Id, Name, (STRUCT('active' AS label)).label
FROM Singers`
	const query = "SELECT v.x FROM ActiveSingers AS v"
	h := newParsedTestHandler(t, "/schema.sql", view)
	addParsedTestDocument(t, h, "/query.sql", query)

	got, err := h.Completion(context.Background(), &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
			Position:     newTextIndex(query).position(len("SELECT v.")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("Completion() items = %#v, want Id, label, and Name", got.Items)
	}
	want := []string{"Id", "label", "Name"}
	for i, label := range want {
		if got.Items[i].Label != label || got.Items[i].Detail != "column of view ActiveSingers" {
			t.Fatalf("Completion() item %d = %#v, want %s view column", i, got.Items[i], label)
		}
	}
}

func TestCompletionRejectsUnknownViewShape(t *testing.T) {
	tests := []struct {
		name string
		view string
	}{
		{
			name: "star",
			view: activeSingersViewDDL,
		},
		{
			name: "anonymous expression",
			view: "CREATE VIEW ActiveSingers SQL SECURITY INVOKER AS SELECT 1 + 1",
		},
		{
			name: "duplicate columns",
			view: "CREATE VIEW ActiveSingers SQL SECURITY INVOKER AS SELECT SingerId, SingerId FROM Singers",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			const query = "SELECT v.x FROM ActiveSingers AS v"
			h := newParsedTestHandler(t, "/schema.sql", test.view)
			addParsedTestDocument(t, h, "/query.sql", query)

			got, err := h.Completion(context.Background(), &protocol.CompletionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: "file:///query.sql"},
					Position:     newTextIndex(query).position(len("SELECT v.")),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Items) != 0 {
				t.Fatalf("Completion() items = %#v, want none for unknown view shape", got.Items)
			}
		})
	}
}
