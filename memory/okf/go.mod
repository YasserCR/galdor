module github.com/YasserCR/galdor/memory/okf

go 1.25.11

require (
	github.com/YasserCR/galdor v1.2.0
	github.com/YasserCR/galdor/memory/sqlite v1.2.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.42.0 // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.50.1 // indirect
)

// During development the parent module and the sqlite backend are resolved
// from the local workspace. These replaces are also respected when building
// this module standalone (e.g. `cd memory/okf && go test ./...`); they are
// ignored by external consumers doing `go get`.
replace github.com/YasserCR/galdor => ../..

replace github.com/YasserCR/galdor/memory/sqlite => ../sqlite
