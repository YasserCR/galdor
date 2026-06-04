package bedrock

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestEmbeddingFamily(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model   string
		family  string
		wantErr bool
	}{
		{"amazon.titan-embed-text-v2:0", "titan", false},
		{"amazon.titan-embed-text-v1", "titan", false},
		{"cohere.embed-multilingual-v3", "cohere", false},
		{"cohere.embed-english-v3", "cohere", false},
		{"anthropic.claude-3-5-haiku-20241022-v1:0", "", true},
		{"", "", true},
	}
	for _, c := range cases {
		fam, err := embeddingFamily(c.model)
		if c.wantErr {
			if err == nil {
				t.Errorf("embeddingFamily(%q): expected error", c.model)
			}
			continue
		}
		if err != nil || fam != c.family {
			t.Errorf("embeddingFamily(%q) = %q, %v; want %q", c.model, fam, err, c.family)
		}
	}
}

func TestNewEmbedder_RequiresRegion(t *testing.T) {
	t.Parallel()
	if _, err := NewEmbedder(EmbedderConfig{}); err == nil {
		t.Fatal("expected error for empty AWS.Region")
	}
}

func TestNewEmbedder_UnsupportedModel(t *testing.T) {
	t.Parallel()
	_, err := NewEmbedder(EmbedderConfig{
		AWS:   aws.Config{Region: "us-east-1"},
		Model: "anthropic.claude-3-5-haiku-20241022-v1:0",
	})
	if err == nil {
		t.Fatal("expected error for non-embedding model")
	}
}

func TestNewEmbedder_Defaults(t *testing.T) {
	t.Parallel()
	e, err := NewEmbedder(EmbedderConfig{AWS: aws.Config{Region: "us-east-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if e.Model() != DefaultEmbeddingModel {
		t.Errorf("Model = %q, want %q", e.Model(), DefaultEmbeddingModel)
	}
	if e.Dimensions() != defaultEmbeddingDim {
		t.Errorf("Dimensions = %d, want %d", e.Dimensions(), defaultEmbeddingDim)
	}
	if e.family != "titan" {
		t.Errorf("family = %q, want titan", e.family)
	}
	if e.cohereInputType != defaultCohereInputType {
		t.Errorf("cohereInputType = %q, want %q", e.cohereInputType, defaultCohereInputType)
	}
}

func TestNewEmbedder_CohereDimAndType(t *testing.T) {
	t.Parallel()
	e, err := NewEmbedder(EmbedderConfig{
		AWS:             aws.Config{Region: "us-east-1"},
		Model:           "cohere.embed-multilingual-v3",
		Dim:             512,
		CohereInputType: "search_query",
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.family != "cohere" {
		t.Errorf("family = %q, want cohere", e.family)
	}
	if e.Dimensions() != 512 {
		t.Errorf("Dimensions = %d, want 512", e.Dimensions())
	}
	if e.cohereInputType != "search_query" {
		t.Errorf("cohereInputType = %q, want search_query", e.cohereInputType)
	}
}

func TestTitanRequest(t *testing.T) {
	t.Parallel()
	b, err := titanRequest("hello world", 1024)
	if err != nil {
		t.Fatal(err)
	}
	var got titanEmbedRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.InputText != "hello world" || got.Dimensions != 1024 || !got.Normalize {
		t.Errorf("titanRequest produced %+v", got)
	}
}

func TestTitanRequest_OmitsZeroDim(t *testing.T) {
	t.Parallel()
	b, _ := titanRequest("x", 0)
	if got := string(b); contains(got, "dimensions") {
		t.Errorf("expected dimensions omitted when 0, got %s", got)
	}
}

func TestParseTitanResponse(t *testing.T) {
	t.Parallel()
	vec, err := parseTitanResponse([]byte(`{"embedding":[0.1,0.2,0.3],"inputTextTokenCount":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 3 || vec[0] != 0.1 {
		t.Errorf("vec = %v", vec)
	}
	if _, err := parseTitanResponse([]byte(`{"embedding":[]}`)); err == nil {
		t.Error("expected error for empty embedding")
	}
}

func TestCohereRequest(t *testing.T) {
	t.Parallel()
	b, err := cohereRequest([]string{"a", "b"}, "search_document")
	if err != nil {
		t.Fatal(err)
	}
	var got cohereEmbedRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Texts) != 2 || got.InputType != "search_document" {
		t.Errorf("cohereRequest produced %+v", got)
	}
}

func TestParseCohereResponse(t *testing.T) {
	t.Parallel()
	vecs, err := parseCohereResponse([]byte(`{"embeddings":[[0.1,0.2],[0.3,0.4]]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 2 || vecs[1][1] != 0.4 {
		t.Errorf("vecs = %v", vecs)
	}
	if _, err := parseCohereResponse([]byte(`{"embeddings":[]}`)); err == nil {
		t.Error("expected error for empty embeddings")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
