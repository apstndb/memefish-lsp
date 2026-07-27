package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/cloudspannerecosystem/memefish"
	"github.com/cloudspannerecosystem/memefish/ast"
)

type documentOrigin uint8

const (
	documentOriginOpen documentOrigin = 1 << iota
	documentOriginWorkspace
)

type successfulDocumentSnapshot struct {
	text       string
	index      textIndex
	statements []ast.Statement
	facts      documentFacts
	revision   uint64
}

type documentSnapshot struct {
	path           string
	text           string
	index          textIndex
	statements     []ast.Statement
	facts          documentFacts
	diagnostics    []protocol.Diagnostic
	parseErr       error
	resultID       string
	version        int32
	revision       uint64
	origin         documentOrigin
	lastSuccessful *successfulDocumentSnapshot
}

func parseDocumentSnapshot(
	path, text string,
	version int32,
	revision uint64,
	origin documentOrigin,
	previous *documentSnapshot,
) *documentSnapshot {
	statements, parseErr := memefish.ParseStatements(path, text)
	index := newTextIndex(text)
	facts := extractDDLFacts(index, statements)
	snapshot := &documentSnapshot{
		path:        path,
		text:        text,
		index:       index,
		statements:  statements,
		facts:       facts,
		diagnostics: diagnosticsFromParseError(parseErr, text),
		parseErr:    parseErr,
		resultID:    fmt.Sprintf("%x", sha256.Sum256([]byte(text))),
		version:     version,
		revision:    revision,
		origin:      origin,
	}
	if previous != nil {
		snapshot.lastSuccessful = previous.lastSuccessful
		if previous.parseErr == nil {
			snapshot.lastSuccessful = successfulSnapshot(previous)
		}
	}
	if parseErr == nil {
		snapshot.lastSuccessful = successfulSnapshot(snapshot)
	}
	return snapshot
}

func successfulSnapshot(snapshot *documentSnapshot) *successfulDocumentSnapshot {
	return &successfulDocumentSnapshot{
		text:       snapshot.text,
		index:      snapshot.index,
		statements: snapshot.statements,
		facts:      snapshot.facts,
		revision:   snapshot.revision,
	}
}

func (h *Handler) documentOriginLocked(path string) documentOrigin {
	var origin documentOrigin
	if snapshot := h.documents[path]; snapshot != nil {
		origin = snapshot.origin
	}
	if _, ok := h.openDocumentMap[path]; ok {
		origin |= documentOriginOpen
	}
	if _, ok := h.workspaceFileMap[path]; ok {
		origin |= documentOriginWorkspace
	}
	return origin
}

func (h *Handler) reserveDocumentUpdate(
	path string,
	mutateOrigin func(documentOrigin) documentOrigin,
) (revision uint64, origin documentOrigin, previous *documentSnapshot) {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	h.documentRevisions[path]++
	revision = h.documentRevisions[path]
	previous = h.documents[path]
	origin = mutateOrigin(h.documentOriginLocked(path))
	return revision, origin, previous
}

func (h *Handler) installDocumentSnapshot(snapshot *documentSnapshot) bool {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()

	if h.documentRevisions[snapshot.path] != snapshot.revision {
		return false
	}
	if snapshot.origin&documentOriginWorkspace != 0 &&
		snapshot.origin&documentOriginOpen == 0 {
		if _, open := h.openDocumentMap[snapshot.path]; open {
			return false
		}
	}
	h.documents[snapshot.path] = snapshot
	h.fileToContentMap[snapshot.path] = []byte(snapshot.text)
	h.parsedMap[snapshot.path] = snapshot.statements
	if snapshot.origin&documentOriginOpen != 0 {
		h.openDocumentMap[snapshot.path] = struct{}{}
	} else {
		delete(h.openDocumentMap, snapshot.path)
	}
	if snapshot.origin&documentOriginWorkspace != 0 {
		h.workspaceFileMap[snapshot.path] = struct{}{}
	} else {
		delete(h.workspaceFileMap, snapshot.path)
	}
	return true
}

func (h *Handler) updateDocument(
	ctx context.Context,
	uri protocol.DocumentURI,
	text string,
	version int32,
	mutateOrigin func(documentOrigin) documentOrigin,
) error {
	path := uri.Path()
	revision, origin, previous := h.reserveDocumentUpdate(path, mutateOrigin)
	snapshot := parseDocumentSnapshot(path, text, version, revision, origin, previous)
	if !h.installDocumentSnapshot(snapshot) {
		return snapshot.parseErr
	}
	publishErr := h.publishDocumentDiagnostics(ctx, uri, snapshot)
	parseErr := snapshot.parseErr
	if parseErr != nil && len(snapshot.diagnostics) == 0 {
		h.logger.Info("unknown error", slog.Any("err", parseErr))
		parseErr = nil
	}
	return errors.Join(parseErr, publishErr)
}

func (h *Handler) publishDocumentDiagnostics(
	ctx context.Context,
	uri protocol.DocumentURI,
	snapshot *documentSnapshot,
) error {
	h.diagnosticPublishMu.Lock()
	defer h.diagnosticPublishMu.Unlock()

	h.fileContentMu.Lock()
	current := h.documents[snapshot.path]
	h.fileContentMu.Unlock()
	if current != snapshot {
		return nil
	}

	client, err := h.Client()
	if err != nil {
		return err
	}
	version := int32(0)
	if snapshot.version > 0 {
		version = snapshot.version
	}
	return client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{
		URI:         uri,
		Version:     version,
		Diagnostics: snapshot.diagnostics,
	})
}

func (h *Handler) documentSnapshot(path string) *documentSnapshot {
	h.fileContentMu.Lock()
	defer h.fileContentMu.Unlock()
	return h.documents[path]
}

func (h *Handler) forgetDocumentLocked(path string) {
	h.documentRevisions[path]++
	delete(h.documents, path)
	delete(h.fileToContentMap, path)
	delete(h.parsedMap, path)
}

func (h *Handler) setDocumentOriginLocked(path string, origin documentOrigin) {
	h.documentRevisions[path]++
	if current := h.documents[path]; current != nil {
		next := *current
		next.origin = origin
		next.revision = h.documentRevisions[path]
		h.documents[path] = &next
	}
	if origin&documentOriginOpen != 0 {
		h.openDocumentMap[path] = struct{}{}
	} else {
		delete(h.openDocumentMap, path)
	}
	if origin&documentOriginWorkspace != 0 {
		h.workspaceFileMap[path] = struct{}{}
	} else {
		delete(h.workspaceFileMap, path)
	}
}
