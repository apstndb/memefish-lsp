package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/apstndb/go-lsp-export/protocol"
)

func TestDocumentStoreRetainsDistinctCurrentSnapshot(t *testing.T) {
	const path = "/test.sql"
	const valid = "SELECT 1"
	const invalid = "SELECT FROM"
	uri := protocol.DocumentURI("file:///test.sql")
	h := NewHandler(slog.Default(), nil)
	client := &recordingClient{}
	h.SetClient(client)

	if err := h.DidOpen(context.Background(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{URI: uri, Version: 1, Text: valid},
	}); err != nil {
		t.Fatal(err)
	}
	first := h.documentSnapshot(path)
	if first == nil || first.parseErr != nil {
		t.Fatalf("first snapshot = %#v, want successful parse", first)
	}

	err := h.DidChange(context.Background(), &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
			Version:                2,
		},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{{Text: invalid}},
	})
	if err == nil {
		t.Fatal("DidChange() succeeded for invalid SQL")
	}

	current := h.documentSnapshot(path)
	if current == nil || current == first || current.text != invalid || current.version != 2 || current.parseErr == nil {
		t.Fatalf("current snapshot = %#v, want distinct invalid version 2 snapshot", current)
	}
	if first.text != valid || first.parseErr != nil {
		t.Fatalf("first snapshot mutated after change: %#v", first)
	}
	if len(client.diagnostics) != 2 || len(client.diagnostics[1].Diagnostics) == 0 {
		t.Fatalf("published diagnostics = %#v, want clear then syntax diagnostics", client.diagnostics)
	}
}

func TestDocumentStoreRejectsStaleInstallAndPublication(t *testing.T) {
	const path = "/test.sql"
	uri := protocol.DocumentURI("file:///test.sql")
	h := NewHandler(slog.Default(), nil)
	client := &recordingClient{}
	h.SetClient(client)

	oldRevision, oldOrigin := h.reserveDocumentUpdate(
		path,
		func(origin documentOrigin) documentOrigin { return origin | documentOriginOpen },
	)
	oldSnapshot := parseDocumentSnapshot(path, "SELECT 1", 1, oldRevision, oldOrigin)
	newRevision, newOrigin := h.reserveDocumentUpdate(
		path,
		func(origin documentOrigin) documentOrigin { return origin | documentOriginOpen },
	)
	newSnapshot := parseDocumentSnapshot(path, "SELECT 2", 2, newRevision, newOrigin)

	if h.installDocumentSnapshot(oldSnapshot) {
		t.Fatal("installed stale snapshot")
	}
	if !h.installDocumentSnapshot(newSnapshot) {
		t.Fatal("failed to install latest snapshot")
	}
	if err := h.publishDocumentDiagnostics(context.Background(), uri, oldSnapshot); err != nil {
		t.Fatal(err)
	}
	if len(client.diagnostics) != 0 {
		t.Fatalf("published stale diagnostics: %#v", client.diagnostics)
	}
}

type stateLockCheckingClient struct {
	protocol.Client
	handler  *Handler
	acquired bool
}

func (client *stateLockCheckingClient) PublishDiagnostics(context.Context, *protocol.PublishDiagnosticsParams) error {
	if !client.handler.fileContentMu.TryLock() {
		return errors.New("document state lock held during PublishDiagnostics")
	}
	client.acquired = true
	client.handler.fileContentMu.Unlock()
	return nil
}

func TestDocumentStorePublishesDiagnosticsOutsideStateLock(t *testing.T) {
	h := NewHandler(slog.Default(), nil)
	client := &stateLockCheckingClient{handler: h}
	h.SetClient(client)

	err := h.DidOpen(context.Background(), &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:     "file:///test.sql",
			Version: 1,
			Text:    "SELECT 1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !client.acquired {
		t.Fatal("PublishDiagnostics was not called")
	}
}

func TestDidChangeAllowsEmptyContentChanges(t *testing.T) {
	h := NewHandler(slog.Default(), nil)

	if err := h.DidChange(context.Background(), &protocol.DidChangeTextDocumentParams{}); err != nil {
		t.Fatal(err)
	}
}
