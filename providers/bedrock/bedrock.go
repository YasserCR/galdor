package bedrock

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/YasserCR/galdor/pkg/provider"
)

// providerName is the stable adapter identifier reported by Name().
const providerName = "bedrock"

// Config configures a Provider. AWS is required and carries credentials,
// region, retry policy and HTTP client. Use config.LoadDefaultConfig from
// github.com/aws/aws-sdk-go-v2/config to build it with the AWS SDK's
// standard credential chain (env vars, ~/.aws/credentials, IAM role,
// SSO, EC2/ECS metadata).
type Config struct {
	// AWS is the SDK configuration used to construct the bedrock-runtime
	// client. Region must be set on it.
	AWS aws.Config

	// ClientOptions lets callers tweak bedrockruntime.Options after the
	// adapter has constructed the client (custom endpoint, retryer,
	// HTTP client). Applied last so they win over defaults.
	ClientOptions []func(*bedrockruntime.Options)
}

// Provider is the Bedrock adapter. Safe for concurrent use.
type Provider struct {
	client *bedrockruntime.Client
	region string
}

// New constructs a Provider from cfg. Returns an error if AWS.Region is
// empty (Bedrock has no global endpoint; a region is required).
func New(cfg Config) (*Provider, error) {
	if cfg.AWS.Region == "" {
		return nil, errors.New("bedrock: AWS.Region is required")
	}
	client := bedrockruntime.NewFromConfig(cfg.AWS, cfg.ClientOptions...)
	return &Provider{client: client, region: cfg.AWS.Region}, nil
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return providerName }

// Capabilities implements provider.Provider.
//
// Bedrock's capabilities depend on the chosen model: Claude on Bedrock
// caches, Llama on Bedrock does not, etc. The reported flags are the
// union of what Converse exposes — callers that need per-model precision
// should consult Bedrock's documentation. MaxContextTokens is reported
// as the long-context tier; some models on Bedrock cap at 200K, others
// at 1M (Claude Sonnet 4.x family) when long-context is enabled.
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:        true,
		ToolCalling:      true,
		StructuredOutput: true,
		PromptCaching:    true,
		VisionInput:      true,
		MaxContextTokens: 200_000,
	}
}

// String reports a developer-friendly description without leaking
// credentials (which the SDK stores in opaque providers anyway).
func (p *Provider) String() string {
	return fmt.Sprintf("bedrock.Provider{region=%q}", p.region)
}

// Sanity: the type must satisfy provider.Provider at compile time.
var _ provider.Provider = (*Provider)(nil)
