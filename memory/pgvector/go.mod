module github.com/YasserCR/galdor/memory/pgvector

go 1.25.11

require (
	github.com/YasserCR/galdor v1.2.0
	github.com/jackc/pgx/v5 v5.9.2
)

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/text v0.29.0 // indirect
)

// During development the parent module is resolved from the local
// workspace. This replace is also respected when building this module
// standalone (e.g. `cd memory/pgvector && go test ./...`).
replace github.com/YasserCR/galdor => ../..
