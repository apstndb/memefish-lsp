module github.com/apstndb/memefish-lsp

go 1.23.1

toolchain go1.23.2

require (
	github.com/apstndb/go-lsp-export v0.0.0-00010101000000-000000000000
	github.com/apstndb/gsqlutils v0.0.0-20241220021154-62754cd04acc
	github.com/cloudspannerecosystem/memefish v0.2.1-0.20250107075614-e6930980bf05
	github.com/samber/lo v1.47.0
	go.lsp.dev/pkg v0.0.0-20210717090340-384b27a52fb2
	go.uber.org/multierr v1.8.0
	go.uber.org/zap v1.21.0
)

require (
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.3.4 // indirect
	go.lsp.dev/uri v0.3.0 // indirect
	go.uber.org/atomic v1.9.0 // indirect
	golang.org/x/exp/event v0.0.0-20220217172124-1812c5b45e43 // indirect
	golang.org/x/exp/jsonrpc2 v0.0.0-20250106191152-7588d65b2ba8 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/telemetry v0.0.0-20250105011419-6d9ea865d014 // indirect
	golang.org/x/text v0.20.0 // indirect
	golang.org/x/tools v0.29.0 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	spheric.cloud/xiter v0.0.0-20240904151420-c999f37a46b2 // indirect
)

replace github.com/apstndb/go-lsp-export => ../go-lsp-export
