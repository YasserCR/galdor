module github.com/YasserCR/galdor/memory/pgvector

go 1.25.0

require (
	github.com/YasserCR/galdor v0.0.0-00010101000000-000000000000
	github.com/jackc/pgx/v5 v5.7.5
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

// During development the parent module is resolved from the local
// workspace. This replace is also respected when building this module
// standalone (e.g. `cd memory/pgvector && go test ./...`).
replace github.com/YasserCR/galdor => ../..
