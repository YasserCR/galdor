module github.com/YasserCR/galdor/providers/google

go 1.25.11

require github.com/YasserCR/galdor v0.13.0

require github.com/google/uuid v1.6.0 // indirect

// During development the parent module is resolved from the local
// workspace. This replace is also respected when building this module
// standalone (e.g. `cd providers/google && go test ./...`).
replace github.com/YasserCR/galdor => ../..
