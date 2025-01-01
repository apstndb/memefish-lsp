package lspabst

import "log/slog"

func New(handler any, logger *slog.Logger) *Wrapper {
	return &Wrapper{
		handler: handler,
		logger:  logger,
	}
}

type Wrapper struct {
	handler any
	logger  *slog.Logger
}
