module github.com/YasserCR/galdor/memory/s3vectors

go 1.25.12

require (
	github.com/YasserCR/galdor v1.2.2
	github.com/aws/aws-sdk-go-v2 v1.42.0
	github.com/aws/aws-sdk-go-v2/config v1.32.21
	github.com/aws/aws-sdk-go-v2/service/s3vectors v1.8.0
)

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.19.20 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.26 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.29 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.27 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.26 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.1.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.31.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.36.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.43.0 // indirect
	github.com/aws/smithy-go v1.27.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
)

// During development the parent module is resolved from the local
// workspace. This replace is also respected when building this module
// standalone (e.g. `cd memory/s3vectors && go test ./...`).
replace github.com/YasserCR/galdor => ../..
