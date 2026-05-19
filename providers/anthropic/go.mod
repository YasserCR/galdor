module github.com/YasserCR/galdor/providers/anthropic

go 1.25.10

require github.com/YasserCR/galdor v0.0.0-00010101000000-000000000000

// During development the parent module is resolved from the local
// workspace. This replace is also respected when building this module
// standalone (e.g. `cd providers/anthropic && go test ./...`).
replace github.com/YasserCR/galdor => ../..
