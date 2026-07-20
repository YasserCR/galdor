module github.com/YasserCR/galdor/providers/anthropic

go 1.25.12

require github.com/YasserCR/galdor v1.2.2

// During development the parent module is resolved from the local
// workspace. This replace is also respected when building this module
// standalone (e.g. `cd providers/anthropic && go test ./...`).
replace github.com/YasserCR/galdor => ../..
