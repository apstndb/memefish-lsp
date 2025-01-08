package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"os"
	"runtime/debug"

	"github.com/apstndb/go-lsp-export/protocol"
	"github.com/samber/lo"
	"golang.org/x/exp/jsonrpc2"

	"github.com/apstndb/memefish-lsp/lspabst"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatalln(err)
	}
}

func run(ctx context.Context) error {
	_, wait := New(ctx)
	return wait()
}

type readWriteCloser struct {
	readCloser  io.ReadCloser
	writeCloser io.WriteCloser
}

func (r *readWriteCloser) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	return &struct {
		io.ReadCloser
		io.Writer
	}{io.NopCloser(r.readCloser), r.writeCloser}, nil
}

type Server struct {
	// conn             *jsonrpc2.Connection
	logFile *os.File
	paths   []string
	// handler          protocol.Server
	logger           *slog.Logger
	afterInitialize  bool
	afterShutdown    bool
	initializeParams *protocol.InitializeParams
}

func New(ctx context.Context) (*Server, func() error) {
	server := &Server{
		logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)),
	}

	handler := NewHandler(server.logger, server.paths)

	conn, err := jsonrpc2.Dial(ctx, &readWriteCloser{
		readCloser:  os.Stdin,
		writeCloser: os.Stdout,
	}, &jsonrpc2.ConnectionOptions{
		Framer:    nil,
		Preempter: server,
		Handler:   protocol.ServerHandler(lspabst.New(handler, server.logger), nil),
	})
	if err != nil {
		return nil, func() error { return err }
	}

	// workaround
	handler.SetClient(protocol.ClientDispatcher(conn))

	return server, func() error {
		return conn.Wait()
	}
}

func (s *Server) Preempt(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	defer func() {
		if err := recover(); err != nil {
			s.logger.Error(
				"recovered",
				slog.Any("error", err),
				slog.String("stack_trace", string(debug.Stack())),
			)
		}
	}()
	if ctx.Err() != nil {
		return nil, protocol.RequestCancelledError
	}

	switch {
	case !req.IsCall(): // notification
		switch {
		case req.Method == protocol.RPCMethodExit:
			os.Exit(lo.Ternary(s.afterShutdown, 0, 1))
		case s.afterShutdown, !s.afterInitialize:
			return nil, nil
		}
	case req.IsCall():
		switch {
		case req.Method == protocol.RPCMethodInitialize:
			s.afterInitialize = true
			params := &protocol.InitializeParams{}
			if err := json.Unmarshal(req.Params, params); err != nil {
				s.logger.Warn("InitializeParams can't be decoded", slog.Any("err", err))
			} else {
				s.initializeParams = params
			}
		case s.afterShutdown:
			return nil, jsonrpc2.ErrInvalidRequest
		case !s.afterInitialize:
			return nil, jsonrpc2.NewError(-30002, "a request arrived before initialize")
		}
	}

	return nil, jsonrpc2.ErrNotHandled
}
