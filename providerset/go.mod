module github.com/YasserCR/galdor/providerset

go 1.25.11

require (
	github.com/YasserCR/galdor v0.13.0
	github.com/YasserCR/galdor/providers/anthropic v0.13.0
	github.com/YasserCR/galdor/providers/bedrock v0.13.0
	github.com/YasserCR/galdor/providers/google v0.13.0
	github.com/YasserCR/galdor/providers/openai v0.13.0
	github.com/aws/aws-sdk-go-v2/config v1.32.21
)

require (
	github.com/aws/aws-sdk-go-v2 v1.41.10 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.11 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.20 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.26 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.26 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.26 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.27 // indirect
	github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.50.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.26 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.1.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.31.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.36.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.43.0 // indirect
	github.com/aws/smithy-go v1.26.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
)

replace (
	github.com/YasserCR/galdor => ..
	github.com/YasserCR/galdor/providers/anthropic => ../providers/anthropic
	github.com/YasserCR/galdor/providers/bedrock => ../providers/bedrock
	github.com/YasserCR/galdor/providers/google => ../providers/google
	github.com/YasserCR/galdor/providers/openai => ../providers/openai
)
