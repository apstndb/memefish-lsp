package lspabst

import (
	"log/slog"

	"go.lsp.dev/protocol"
)

func New(handler any, logger *slog.Logger) *Wrapper {
	return &Wrapper{
		handler: handler,
		logger:  logger,
	}
}

var _ protocol.Server = (*Wrapper)(nil)

type Wrapper struct {
	handler any
	logger  *slog.Logger
}

type TextDocumentSyncCapability interface {
	CanDidOpen
	CanDidChange
	CanDidClose
}
