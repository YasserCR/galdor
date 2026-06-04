//go:build integration

package bedrock

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
)

// TestEmbedder_Integration hits real AWS Bedrock and consumes a tiny amount of
// tokens. It runs only under the "integration" build tag and when the AWS SDK
// can resolve credentials. The embedding model is taken from
// BEDROCK_TEST_EMBED_MODEL_ID (default: amazon.titan-embed-text-v2:0).
//
//	export AWS_REGION=us-east-1
//	export AWS_ACCESS_KEY_ID=...   AWS_SECRET_ACCESS_KEY=...
//	# optional: export BEDROCK_TEST_EMBED_MODEL_ID=cohere.embed-multilingual-v3
//	go test -tags=integration ./providers/bedrock/ -run Embedder_Integration -v
func TestEmbedder_Integration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		t.Skipf("no AWS config: %v", err)
	}
	if awsCfg.Region == "" {
		t.Skip("AWS_REGION not set")
	}

	model := os.Getenv("BEDROCK_TEST_EMBED_MODEL_ID")
	if model == "" {
		model = DefaultEmbeddingModel
	}

	e, err := NewEmbedder(EmbedderConfig{AWS: awsCfg, Model: model})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}

	texts := []string{
		"El Servicio de Administración Tributaria recauda impuestos federales.",
		"El IMSS otorga pensiones y prestaciones médicas.",
	}
	vecs, err := e.Embed(ctx, texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("got %d vectors for %d texts", len(vecs), len(texts))
	}
	for i, v := range vecs {
		if len(v) != e.Dimensions() {
			t.Errorf("vector %d has %d dims, want %d", i, len(v), e.Dimensions())
		}
	}
	t.Logf("model=%s dims=%d vectors=%d", e.Model(), e.Dimensions(), len(vecs))
}
