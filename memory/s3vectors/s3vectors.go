package s3vectors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3vectors"
	"github.com/aws/aws-sdk-go-v2/service/s3vectors/document"
	"github.com/aws/aws-sdk-go-v2/service/s3vectors/types"

	"github.com/YasserCR/galdor/pkg/memory"
)

// Reserved metadata keys used to round-trip the Chunk fields that are
// not part of the user's metadata. The double-underscore prefix
// signals "system-owned" and avoids collisions with caller-supplied
// keys. Chunk.ID is not stored here — it is the vector's key.
const (
	metaKeyDocumentID = "__document_id"
	metaKeyIndex      = "__index"
	metaKeyText       = "__text"
)

// API limits (per the S3 Vectors service quotas). PutVectors and
// DeleteVectors accept up to 500 vectors/keys per request; ListVectors
// returns up to 1000 per page; QueryVectors accepts topK up to 10000
// but returns at most 100 results per page (so Retrieve paginates).
const (
	maxPutBatch    = 500
	maxDeleteBatch = 500
	listPageSize   = 1000
)

// api is the subset of the S3 Vectors client used by this Store.
// *s3vectors.Client satisfies it; tests inject a fake.
type api interface {
	GetIndex(context.Context, *s3vectors.GetIndexInput, ...func(*s3vectors.Options)) (*s3vectors.GetIndexOutput, error)
	CreateIndex(context.Context, *s3vectors.CreateIndexInput, ...func(*s3vectors.Options)) (*s3vectors.CreateIndexOutput, error)
	PutVectors(context.Context, *s3vectors.PutVectorsInput, ...func(*s3vectors.Options)) (*s3vectors.PutVectorsOutput, error)
	QueryVectors(context.Context, *s3vectors.QueryVectorsInput, ...func(*s3vectors.Options)) (*s3vectors.QueryVectorsOutput, error)
	DeleteVectors(context.Context, *s3vectors.DeleteVectorsInput, ...func(*s3vectors.Options)) (*s3vectors.DeleteVectorsOutput, error)
	ListVectors(context.Context, *s3vectors.ListVectorsInput, ...func(*s3vectors.Options)) (*s3vectors.ListVectorsOutput, error)
}

// Config configures the S3 Vectors Store.
type Config struct {
	// Bucket is the S3 Vectors vector-bucket name. Required. The bucket
	// must already exist; Open does not create it.
	Bucket string

	// Index is the vector index name. Defaults to "galdor-chunks".
	// Created on Open if missing. S3 Vectors index names are DNS-style:
	// 3–63 chars, lowercase letters/digits/hyphens, no underscores.
	Index string

	// Region is the AWS region. When empty the region is resolved from
	// the default AWS config chain (AWS_REGION, shared config, ...).
	Region string

	// Dim is the embedding dimensionality (e.g. 1024 for Titan v2).
	// Required. Must match the index when it already exists.
	Dim int

	// Distance is the distance metric used when creating the index.
	// Defaults to cosine. Ignored when the index already exists.
	Distance types.DistanceMetric
}

// Store is a memory.Store backed by Amazon S3 Vectors. The zero value
// is not usable; call Open.
type Store struct {
	api      api
	bucket   string
	index    string
	dim      int
	distance types.DistanceMetric
}

var _ memory.Store = (*Store)(nil)

// Open returns a usable Store. It loads AWS config via the default
// credential chain, validates the config and ensures the index exists
// (creating it if missing; the bucket must already exist).
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("memory/s3vectors: Bucket is required")
	}
	if cfg.Dim <= 0 {
		return nil, errors.New("memory/s3vectors: Dim must be > 0")
	}
	if cfg.Index == "" {
		cfg.Index = "galdor-chunks"
	}
	if err := validateIndexName(cfg.Index); err != nil {
		return nil, err
	}
	if cfg.Distance == "" {
		cfg.Distance = types.DistanceMetricCosine
	}

	var loadOpts []func(*awsconfig.LoadOptions) error
	if cfg.Region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(cfg.Region))
	}
	awscfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("memory/s3vectors: load AWS config: %w", err)
	}

	s := &Store{
		api:      s3vectors.NewFromConfig(awscfg),
		bucket:   cfg.Bucket,
		index:    cfg.Index,
		dim:      cfg.Dim,
		distance: cfg.Distance,
	}
	if err := s.ensureIndex(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Close releases store-owned resources. The AWS SDK client owns its
// own pooled transport, so this is a no-op; safe to call repeatedly.
func (*Store) Close() error { return nil }

// ensureIndex probes the index and creates it if missing. When the
// index already exists its dimension is validated against the config.
func (s *Store) ensureIndex(ctx context.Context) error {
	out, err := s.api.GetIndex(ctx, &s3vectors.GetIndexInput{
		VectorBucketName: aws.String(s.bucket),
		IndexName:        aws.String(s.index),
	})
	if err == nil {
		if out.Index != nil && out.Index.Dimension != nil && int(*out.Index.Dimension) != s.dim {
			return fmt.Errorf("memory/s3vectors: index %q is %d-dim; Config.Dim is %d", s.index, *out.Index.Dimension, s.dim)
		}
		return nil
	}

	var notFound *types.NotFoundException
	if !errors.As(err, &notFound) {
		return fmt.Errorf("memory/s3vectors: probe index %q in bucket %q: %w", s.index, s.bucket, err)
	}

	// Bucket must already exist; a missing bucket surfaces here as an error.
	_, err = s.api.CreateIndex(ctx, &s3vectors.CreateIndexInput{
		VectorBucketName: aws.String(s.bucket),
		IndexName:        aws.String(s.index),
		DataType:         types.DataTypeFloat32,
		Dimension:        aws.Int32(int32(s.dim)),
		DistanceMetric:   s.distance,
		MetadataConfiguration: &types.MetadataConfiguration{
			// __text can be large; keep it out of the (size-limited)
			// filterable metadata. User metadata stays filterable.
			NonFilterableMetadataKeys: []string{metaKeyText},
		},
	})
	if err != nil {
		return fmt.Errorf("memory/s3vectors: create index %q in bucket %q (does the bucket exist?): %w", s.index, s.bucket, err)
	}
	return nil
}

// Add upserts chunks into the index, keyed by Chunk.ID (stable id →
// idempotent re-ingest). Every chunk must have a non-empty ID and an
// embedding whose length matches the index dimension. PutVectors calls
// are batched.
func (s *Store) Add(ctx context.Context, chunks []memory.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	vectors := make([]types.PutInputVector, 0, len(chunks))
	for _, c := range chunks {
		if c.ID == "" {
			return errors.New("memory/s3vectors: Chunk.ID is empty (caller must assign IDs)")
		}
		if len(c.Embedding) != s.dim {
			return fmt.Errorf("memory/s3vectors: chunk %q has %d-dim embedding; index is %d-dim", c.ID, len(c.Embedding), s.dim)
		}
		meta, err := buildMetadata(c)
		if err != nil {
			return err
		}
		// Copy the embedding: the caller may reuse/mutate the slice and
		// the SDK retains it until the request is serialized.
		vec := make([]float32, len(c.Embedding))
		copy(vec, c.Embedding)
		vectors = append(vectors, types.PutInputVector{
			Key:      aws.String(c.ID),
			Data:     &types.VectorDataMemberFloat32{Value: vec},
			Metadata: meta,
		})
	}

	for start := 0; start < len(vectors); start += maxPutBatch {
		end := min(start+maxPutBatch, len(vectors))
		_, err := s.api.PutVectors(ctx, &s3vectors.PutVectorsInput{
			VectorBucketName: aws.String(s.bucket),
			IndexName:        aws.String(s.index),
			Vectors:          vectors[start:end],
		})
		if err != nil {
			return fmt.Errorf("memory/s3vectors: put vectors: %w", err)
		}
	}
	return nil
}

// Retrieve runs a top-K cosine nearest-neighbor query. Requires
// q.Embedding (this backend is vector-only). q.Filter is pushed to
// S3 Vectors as an exact-match AND over metadata.
func (s *Store) Retrieve(ctx context.Context, q memory.Query) ([]memory.Result, error) {
	if len(q.Embedding) == 0 {
		return nil, errors.New("memory/s3vectors: Query.Embedding is required (this backend is vector-only)")
	}
	if len(q.Embedding) != s.dim {
		return nil, fmt.Errorf("memory/s3vectors: query has %d-dim embedding; index is %d-dim", len(q.Embedding), s.dim)
	}
	k := q.K
	if k <= 0 {
		k = 5
	}

	in := &s3vectors.QueryVectorsInput{
		VectorBucketName: aws.String(s.bucket),
		IndexName:        aws.String(s.index),
		QueryVector:      &types.VectorDataMemberFloat32{Value: q.Embedding},
		TopK:             aws.Int32(int32(k)),
		ReturnDistance:   true,
		ReturnMetadata:   true,
	}
	if f := buildFilter(q.Filter); f != nil {
		in.Filter = f
	}

	// QueryVectors returns at most 100 results per page even though TopK
	// may be larger, so page through NextToken until the requested K is
	// assembled (or the service runs out).
	results := make([]memory.Result, 0, k)
	for {
		out, err := s.api.QueryVectors(ctx, in)
		if err != nil {
			return nil, fmt.Errorf("memory/s3vectors: query vectors: %w", err)
		}
		// The metric is reported by the service; fall back to the configured
		// one if the response omits it. It governs the distance→score map.
		metric := out.DistanceMetric
		if metric == "" {
			metric = s.distance
		}
		for _, v := range out.Vectors {
			c := chunkFromVector(v.Key, v.Metadata)
			var dist float32
			if v.Distance != nil {
				dist = *v.Distance
			}
			if metric == types.DistanceMetricEuclidean {
				// Euclidean distance ∈ [0, +∞) (S3 Vectors returns squared
				// L2): map to a monotone-decreasing similarity in (0, 1] with
				// no negative cutoff, so far neighbors keep a meaningful rank.
				results = append(results, memory.Result{Chunk: c, Score: 1 / (1 + dist)})
				continue
			}
			// Cosine distance ∈ [0, 2] → cosine similarity ∈ [-1, 1]. Drop
			// anti-correlated hits for parity with the other backends.
			score := 1 - dist
			if score < 0 {
				continue
			}
			results = append(results, memory.Result{Chunk: c, Score: score})
		}
		if out.NextToken == nil || len(results) >= k {
			break
		}
		in.NextToken = out.NextToken
	}
	if len(results) > k {
		results = results[:k]
	}
	return results, nil
}

// Delete removes every chunk whose __document_id matches documentID.
// S3 Vectors has no delete-by-filter, so the index is scanned and the
// matching keys are batch-deleted. Deleting a non-existent document is
// a no-op.
func (s *Store) Delete(ctx context.Context, documentID string) error {
	if documentID == "" {
		return errors.New("memory/s3vectors: Delete called with empty documentID")
	}

	var keys []string
	var next *string
	for {
		out, err := s.api.ListVectors(ctx, &s3vectors.ListVectorsInput{
			VectorBucketName: aws.String(s.bucket),
			IndexName:        aws.String(s.index),
			ReturnMetadata:   true,
			MaxResults:       aws.Int32(listPageSize),
			NextToken:        next,
		})
		if err != nil {
			return fmt.Errorf("memory/s3vectors: list vectors: %w", err)
		}
		for _, v := range out.Vectors {
			if v.Key == nil {
				continue
			}
			if metadataString(v.Metadata, metaKeyDocumentID) == documentID {
				keys = append(keys, *v.Key)
			}
		}
		if out.NextToken == nil {
			break
		}
		next = out.NextToken
	}

	for start := 0; start < len(keys); start += maxDeleteBatch {
		end := min(start+maxDeleteBatch, len(keys))
		_, err := s.api.DeleteVectors(ctx, &s3vectors.DeleteVectorsInput{
			VectorBucketName: aws.String(s.bucket),
			IndexName:        aws.String(s.index),
			Keys:             keys[start:end],
		})
		if err != nil {
			return fmt.Errorf("memory/s3vectors: delete vectors: %w", err)
		}
	}
	return nil
}

// Len reports the number of vectors in the index. Not part of the
// Store interface; useful for tests. It scans the index (paginated),
// so it is O(index size).
func (s *Store) Len(ctx context.Context) (int, error) {
	count := 0
	var next *string
	for {
		out, err := s.api.ListVectors(ctx, &s3vectors.ListVectorsInput{
			VectorBucketName: aws.String(s.bucket),
			IndexName:        aws.String(s.index),
			MaxResults:       aws.Int32(listPageSize),
			NextToken:        next,
		})
		if err != nil {
			return 0, fmt.Errorf("memory/s3vectors: list vectors: %w", err)
		}
		count += len(out.Vectors)
		if out.NextToken == nil {
			break
		}
		next = out.NextToken
	}
	return count, nil
}

// validateIndexName rejects names S3 Vectors would reject, with a clear
// message, instead of letting the caller discover it as an opaque 400
// after a round-trip. Rule (per S3 Vectors): 3–63 chars; lowercase
// letters, digits, '-' and '.'; must begin and end with a letter or
// digit. No underscores or uppercase — a common slip when carrying over
// the pgvector/qdrant table/collection naming convention.
func validateIndexName(name string) error {
	if len(name) < 3 || len(name) > 63 {
		return fmt.Errorf("memory/s3vectors: index name %q must be 3–63 characters", name)
	}
	isAlnum := func(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') }
	if !isAlnum(name[0]) || !isAlnum(name[len(name)-1]) {
		return fmt.Errorf("memory/s3vectors: index name %q must begin and end with a lowercase letter or digit", name)
	}
	for i := 0; i < len(name); i++ {
		if b := name[i]; !isAlnum(b) && b != '-' && b != '.' {
			return fmt.Errorf("memory/s3vectors: index name %q has invalid character %q (allowed: a-z, 0-9, '-', '.'; no underscores or uppercase)", name, string(b))
		}
	}
	return nil
}

// buildMetadata assembles the metadata document for a chunk: the
// reserved Chunk fields plus the user's metadata. A user key using the
// reserved "__" prefix is rejected so it cannot clobber the chunk's
// identity on read.
func buildMetadata(c memory.Chunk) (document.Interface, error) {
	m := map[string]any{
		metaKeyDocumentID: c.DocumentID,
		metaKeyIndex:      c.Index,
		metaKeyText:       c.Text,
	}
	for k, v := range c.Metadata {
		if strings.HasPrefix(k, "__") {
			return nil, fmt.Errorf("memory/s3vectors: chunk %q metadata key %q uses the reserved %q prefix", c.ID, k, "__")
		}
		m[k] = v
	}
	return document.NewLazyDocument(m), nil
}

// buildFilter turns a galdor exact-match filter into an S3 Vectors
// metadata filter document. Returns nil when the filter is empty.
//
// A single key uses the bare implicit-$eq form {"k":"v"}. S3 Vectors
// does NOT accept implicit AND across multiple top-level keys (it 400s
// with "Invalid filter"), so two or more pairs are combined with the
// explicit $and operator: {"$and":[{"k1":"v1"},{"k2":"v2"}]}.
func buildFilter(filter map[string]string) document.Interface {
	switch len(filter) {
	case 0:
		return nil
	case 1:
		m := make(map[string]any, 1)
		for k, v := range filter {
			m[k] = v
		}
		return document.NewLazyDocument(m)
	default:
		conds := make([]any, 0, len(filter))
		for k, v := range filter {
			conds = append(conds, map[string]any{k: v})
		}
		return document.NewLazyDocument(map[string]any{"$and": conds})
	}
}

// chunkFromVector reconstructs a Chunk from a returned vector's key and
// metadata document, stripping the reserved keys and routing the rest
// to Metadata. The Embedding is not populated (not requested back).
func chunkFromVector(key *string, meta document.Interface) memory.Chunk {
	c := memory.Chunk{}
	if key != nil {
		c.ID = *key
	}
	raw := decodeMetadata(meta)
	if raw == nil {
		return c
	}
	if v, ok := raw[metaKeyDocumentID].(string); ok {
		c.DocumentID = v
	}
	if v, ok := raw[metaKeyText].(string); ok {
		c.Text = v
	}
	c.Index = toInt(raw[metaKeyIndex])

	var md map[string]string
	for k, v := range raw {
		if strings.HasPrefix(k, "__") {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if md == nil {
			md = map[string]string{}
		}
		md[k] = s
	}
	c.Metadata = md
	return c
}

// metadataString decodes one string-valued metadata key from a vector's
// metadata document; returns "" when absent or non-string.
func metadataString(meta document.Interface, key string) string {
	raw := decodeMetadata(meta)
	s, _ := raw[key].(string)
	return s
}

// decodeMetadata unmarshals a metadata document into a generic map.
// Returns nil on a nil document or decode error.
//
// It goes through MarshalSmithyDocument + json.Unmarshal rather than
// the document's UnmarshalSmithyDocument: both the response-side
// (documentUnmarshaler) and any locally-built (documentMarshaler/
// NewLazyDocument) document marshal to plain JSON, so this path is
// identical in production and in tests, and sidesteps the lazy
// document's idiosyncratic unmarshal-into-map behavior.
func decodeMetadata(meta document.Interface) map[string]any {
	if meta == nil {
		return nil
	}
	b, err := meta.MarshalSmithyDocument()
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}
	return raw
}

// toInt coerces a metadata number (decoded from a document into an
// untyped value) to int, tolerating the several shapes the smithy
// document decoder may produce.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}
