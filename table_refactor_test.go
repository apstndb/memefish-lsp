package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish"
)

func TestRenameEditsAllKnownPhysicalTableSites(t *testing.T) {
	const path = "/test.sql"
	const text = `CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);
SELECT * FROM Singers;
INSERT INTO Singers (SingerId) VALUES (1);
UPDATE Singers SET SingerId = 2 WHERE TRUE;
DELETE FROM Singers WHERE TRUE`
	h := newParsedTestHandler(t, path, text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 1, Character: 16},
		},
		NewName: "Artists",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("Rename() returned nil, want complete physical-table edit")
	}
	edits := got.Changes["file:///test.sql"]
	if len(edits) != 5 {
		t.Fatalf("Rename() returned %d edits, want CREATE/SELECT/INSERT/UPDATE/DELETE: %#v", len(edits), edits)
	}
	for i, edit := range edits {
		if edit.NewText != "Artists" {
			t.Fatalf("Rename() edit %d NewText = %q, want Artists", i, edit.NewText)
		}
		if edit.Range.Start.Line != uint32(i) {
			t.Fatalf("Rename() edit %d range = %#v, want line %d", i, edit.Range, i)
		}
	}
}

func TestRenameFailsClosedForCTEShadowing(t *testing.T) {
	const path = "/test.sql"
	const text = `CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);
WITH Singers AS (SELECT 1 AS SingerId) SELECT * FROM Singers`
	h := newParsedTestHandler(t, path, text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 0, Character: 15},
		},
		NewName: "Artists",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want fail-closed nil for CTE-shadowed table use", got)
	}
}

func TestRenameFailsClosedForDuplicateDeclarations(t *testing.T) {
	const path = "/test.sql"
	const text = `CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);
CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)`
	h := newParsedTestHandler(t, path, text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 0, Character: 15},
		},
		NewName: "Artists",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want fail-closed nil for duplicate declarations", got)
	}
}

func TestRenameFailsClosedForAmbiguousPathTableExpr(t *testing.T) {
	const path = "/test.sql"
	const text = `CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);
SELECT * FROM Singers.Children`
	h := newParsedTestHandler(t, path, text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 0, Character: 15},
		},
		NewName: "Artists",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want fail-closed nil for PathTableExpr", got)
	}
}

func TestRenameFailsClosedAtTableIdentityBoundary(t *testing.T) {
	const path = "/test.sql"
	const text = `CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);
ALTER TABLE Singers RENAME TO Artists`
	h := newParsedTestHandler(t, path, text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 0, Character: 15},
		},
		NewName: "Performers",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want fail-closed nil across rename history", got)
	}
}

func TestRenameFailsClosedAcrossWorkspaceRoots(t *testing.T) {
	h := newParsedTestHandler(t, "/workspace-a/schema.sql", "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId)")
	addParsedTestDocument(t, h, "/workspace-b/query.sql", "SELECT * FROM Singers")
	h.workspaceRootMap["/workspace-a"] = struct{}{}
	h.workspaceRootMap["/workspace-b"] = struct{}{}

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///workspace-a/schema.sql"},
			Position:     protocol.Position{Character: 15},
		},
		NewName: "Artists",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want fail-closed nil across workspace roots", got)
	}
}

func TestCollectTableRefactorSitesIncludesKnownDDLReferences(t *testing.T) {
	tests := []string{
		"ALTER TABLE Singers ADD COLUMN Name STRING(MAX)",
		"DROP TABLE Singers",
		"CREATE INDEX SingersByName ON Singers(Name)",
		"CREATE VECTOR INDEX SingersEmbedding ON Singers(Embedding) OPTIONS(distance_type = 'COSINE')",
		"CREATE SEARCH INDEX SingersSearch ON Singers(TokenList)",
		"CREATE TABLE Albums (AlbumId INT64, SingerId INT64, CONSTRAINT FK FOREIGN KEY (SingerId) REFERENCES Singers (SingerId)) PRIMARY KEY (AlbumId)",
		"CREATE TABLE Albums (AlbumId INT64) PRIMARY KEY (AlbumId), INTERLEAVE IN PARENT Singers ON DELETE CASCADE",
		"CREATE CHANGE STREAM SingerChanges FOR Singers",
		"GRANT SELECT ON TABLE Singers TO ROLE reader",
		"CREATE PROPERTY GRAPH SingerGraph NODE TABLES (Singers)",
	}
	for _, text := range tests {
		t.Run(text, func(t *testing.T) {
			stmts, err := memefish.ParseStatements("/test.sql", text)
			if err != nil {
				t.Fatal(err)
			}
			sites, declarations, safe := collectTableRefactorSites(newLexer("/test.sql", text), stmts, "Singers")
			if !safe || declarations != 0 || len(sites) != 1 {
				t.Fatalf("collectTableRefactorSites() = sites %#v, declarations %d, safe %t; want one safe reference", sites, declarations, safe)
			}
		})
	}
}

func TestRenameFailsClosedForQuotedReference(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nSELECT * FROM `Singers`"
	h := newParsedTestHandler(t, path, text)

	got, err := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 0, Character: 15},
		},
		NewName: "Artists",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want fail-closed nil for quoted reference", got)
	}
}

func TestRenameFailsClosedForTargetInBadNode(t *testing.T) {
	const path = "/test.sql"
	const text = "CREATE TABLE Singers (SingerId INT64) PRIMARY KEY (SingerId);\nBROKEN Singers"
	h := NewHandler(slog.Default(), nil)
	parsed, err := memefish.ParseStatements(path, text)
	if err == nil {
		t.Fatal("ParseStatements() succeeded, want recoverable bad statement")
	}
	h.fileToContentMap[path] = []byte(text)
	h.parsedMap[path] = parsed

	got, renameErr := h.Rename(context.Background(), &protocol.RenameParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: "file:///test.sql"},
			Position:     protocol.Position{Line: 0, Character: 15},
		},
		NewName: "Artists",
	})
	if renameErr != nil {
		t.Fatal(renameErr)
	}
	if got != nil {
		t.Fatalf("Rename() = %#v, want fail-closed nil for target in BadNode", got)
	}
}
