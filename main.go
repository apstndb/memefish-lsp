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
	"go.uber.org/multierr"
	// "go.lsp.dev/jsonrpc2"
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

func (r *readWriteCloser) Read(b []byte) (int, error) {
	return r.readCloser.Read(b)
}

func (r *readWriteCloser) Write(b []byte) (int, error) {
	return r.writeCloser.Write(b)
}

func (r *readWriteCloser) Close() error {
	return multierr.Append(r.readCloser.Close(), r.writeCloser.Close())
}

type Server struct {
	// conn             *jsonrpc2.Connection
	logFile          *os.File
	paths            []string
	handler          protocol.Server
	logger           *slog.Logger
	afterInitialize  bool
	afterShutdown    bool
	initializeParams *protocol.InitializeParams
}

func New(ctx context.Context) (*Server, func() error) {
	server := &Server{}
	writer := os.Stderr
	if server.logFile != nil {
		writer = server.logFile
	}

	logger := slog.New(slog.NewJSONHandler(writer, nil))
	server.logger = logger
	rawHandler := NewHandler(logger, server.paths)
	server.handler = lspabst.New(rawHandler, logger)

	conn, err := jsonrpc2.Dial(ctx, &readWriteCloser{
		readCloser:  os.Stdin,
		writeCloser: os.Stdout,
	}, &jsonrpc2.ConnectionOptions{
		Framer:    nil,
		Preempter: server,
		Handler:   protocol.ServerHandler(server.handler, nil),
	})

	if err != nil {
		return nil, func() error { return err }
	}

	// workaround
	rawHandler.SetClient(protocol.ClientDispatcher(conn))

	/*
		for _, opt := range opts {
			opt(server)
		}
	*/
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
