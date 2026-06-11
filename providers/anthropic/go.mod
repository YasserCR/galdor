module github.com/YasserCR/galdor/providers/anthropic

go 1.25.11

require github.com/YasserCR/galdor v0.9.1

// During development the parent module is resolved from the local
// workspace. This replace is also respected when building this module
// standalone (e.g. `cd providers/anthropic && go test ./...`).
replace github.com/YasserCR/galdor => ../..
