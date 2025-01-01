package main

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"runtime/debug"

	"github.com/samber/lo"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/pkg/xcontext"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/apstndb/memefish-lsp/lspabst"
)
import "go.lsp.dev/protocol"

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatalln(err)
	}
}

func run(ctx context.Context) error {
	server := New()
	server.Run(ctx)
	return nil
}

type readWriteCloser struct {
	readCloser  io.ReadCloser
	writeCloser io.WriteCloser
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
	conn            jsonrpc2.Conn
	logFile         *os.File
	paths           []string
	handler         protocol.Server
	logger          *slog.Logger
	afterInitialize bool
	afterShutdown   bool
}

func New() *Server {
	conn := jsonrpc2.NewConn(
		jsonrpc2.NewStream(
			&readWriteCloser{
				readCloser:  os.Stdin,
				writeCloser: os.Stdout,
			},
		),
	)
	client := protocol.ClientDispatcher(conn, zap.NewNop())

	server := &Server{conn: conn}
	/*
		for _, opt := range opts {
			opt(server)
		}
	*/
	writer := os.Stderr
	if server.logFile != nil {
		writer = server.logFile
	}

	logger := slog.New(slog.NewJSONHandler(writer, nil))
	server.logger = logger
	server.handler = lspabst.New(NewHandler(client, logger, server.paths), logger)
	return server
}

func (s *Server) customServerHandler() jsonrpc2.Handler {
	orgHandler := protocol.ServerHandler(s.handler, nil)

	return func(ctx context.Context, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
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
			xctx := xcontext.Detach(ctx)
			return reply(xctx, nil, protocol.ErrRequestCancelled)
		}

		switch r := req.(type) {
		case *jsonrpc2.Notification:
			switch {
			case req.Method() == protocol.MethodExit:
				os.Exit(lo.Ternary(s.afterShutdown, 0, 1))
			case s.afterShutdown, !s.afterInitialize:
				return nil
			}
		case *jsonrpc2.Call:
			switch {
			case r.Method() == protocol.MethodInitialize:
				s.afterInitialize = true
			case s.afterShutdown:
				return jsonrpc2.ErrInvalidRequest
			case !s.afterInitialize:
				return jsonrpc2.NewError(jsonrpc2.Code(-30002), "a request arrived before initialize")
			}
		}

		return orgHandler(ctx, reply, req)
	}
}

func (s *Server) Run(ctx context.Context) {
	s.conn.Go(ctx, s.customServerHandler())
	<-s.conn.Done()
}
