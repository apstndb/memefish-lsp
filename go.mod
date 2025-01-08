module github.com/apstndb/memefish-lsp

go 1.23.1

toolchain go1.23.2

require (
	github.com/apstndb/go-lsp-export v0.0.0-20250108105656-59a3e5076bce
	github.com/apstndb/gsqlutils v0.0.0-20241220021154-62754cd04acc
	github.com/cloudspannerecosystem/memefish v0.2.1-0.20250107075614-e6930980bf05
	github.com/samber/lo v1.47.0
	golang.org/x/exp/jsonrpc2 v0.0.0-20250106191152-7588d65b2ba8
)

require (
	golang.org/x/exp/event v0.0.0-20220217172124-1812c5b45e43 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/telemetry v0.0.0-20250105011419-6d9ea865d014 // indirect
	golang.org/x/text v0.20.0 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	spheric.cloud/xiter v0.0.0-20240904151420-c999f37a46b2 // indirect
)

replace github.com/cloudspannerecosystem/memefish => github.com/apstndb/memefish v0.0.0-20250108113653-810cde867e02
