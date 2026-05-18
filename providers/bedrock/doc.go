// Package bedrock implements the galdor Provider abstraction against
// AWS Bedrock's Converse API (bedrock-runtime). It targets the unified
// Converse / ConverseStream surface — not the older per-vendor
// InvokeModel — so the same code paths support Anthropic Claude,
// Amazon Nova, Meta Llama, Mistral, Cohere, AI21 and any other model
// family Bedrock chooses to expose through Converse.
//
// Unlike the other galdor adapters this one depends on the official
// AWS SDK for Go v2. The dependency lives only inside this module
// (galdor's core stays SDK-free), but the win is large: SigV4 request
// signing, credential chain resolution, retry handling, and the AWS
// Event Stream binary framing used by ConverseStream are non-trivial
// and the SDK already implements them correctly.
//
// Usage:
//
//	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
//	if err != nil { ... }
//	p, err := bedrock.New(bedrock.Config{AWS: awsCfg})
//	if err != nil { ... }
//	resp, err := p.Generate(ctx, provider.Request{
//	    Model:    "anthropic.claude-3-7-sonnet-20250219-v1:0",
//	    Messages: []schema.Message{schema.UserMessage("hello")},
//	})
//
// The Model field of provider.Request carries the Bedrock model ID (or
// inference-profile ARN); the adapter forwards it unchanged.
package bedrock
