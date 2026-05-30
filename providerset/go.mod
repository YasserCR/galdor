module github.com/YasserCR/galdor/providerset

go 1.25.10

require (
	github.com/YasserCR/galdor v0.3.1
	github.com/YasserCR/galdor/providers/anthropic v0.3.1
	github.com/YasserCR/galdor/providers/bedrock v0.3.1
	github.com/YasserCR/galdor/providers/google v0.3.1
	github.com/YasserCR/galdor/providers/openai v0.3.1
	github.com/aws/aws-sdk-go-v2/config v1.32.17
)

require (
	github.com/aws/aws-sdk-go-v2 v1.41.7 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.10 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.16 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.23 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.24 // indirect
	github.com/aws/aws-sdk-go-v2/service/bedrockruntime v1.50.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.23 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.11 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.42.1 // indirect
	github.com/aws/smithy-go v1.25.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
)

replace (
	github.com/YasserCR/galdor => ..
	github.com/YasserCR/galdor/providers/anthropic => ../providers/anthropic
	github.com/YasserCR/galdor/providers/bedrock => ../providers/bedrock
	github.com/YasserCR/galdor/providers/google => ../providers/google
	github.com/YasserCR/galdor/providers/openai => ../providers/openai
)
