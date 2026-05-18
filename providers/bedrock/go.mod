module github.com/YasserCR/galdor/providers/bedrock

go 1.25

// During development the parent module is resolved from the local
// workspace. This replace is also respected when building this module
// standalone.
replace github.com/YasserCR/galdor => ../..

require (
	github.com/YasserCR/galdor v0.0.0-00010101000000-000000000000
	github.com/aws/aws-sdk-go-v2 v1.41.7
	github.com/aws/aws-sdk-go-v2/credentials v1.19.16
	github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.50.6
	github.com/aws/smithy-go v1.25.1
)

require (
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.10 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.23 // indirect
)
