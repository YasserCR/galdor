package s3vectors

import (
	"context"
	"encoding/json"
	"maps"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3vectors"
	"github.com/aws/aws-sdk-go-v2/service/s3vectors/document"
	"github.com/aws/aws-sdk-go-v2/service/s3vectors/types"

	"github.com/YasserCR/galdor/pkg/memory"
)

// fakeAPI is an in-memory stand-in for the S3 Vectors client. Each
// field, when set, handles the corresponding call; a nil field with an
// unexpected call fails the test via the default branch.
type fakeAPI struct {
	getIndex      func(*s3vectors.GetIndexInput) (*s3vectors.GetIndexOutput, error)
	createIndex   func(*s3vectors.CreateIndexInput) (*s3vectors.CreateIndexOutput, error)
	putVectors    func(*s3vectors.PutVectorsInput) (*s3vectors.PutVectorsOutput, error)
	queryVectors  func(*s3vectors.QueryVectorsInput) (*s3vectors.QueryVectorsOutput, error)
	deleteVectors func(*s3vectors.DeleteVectorsInput) (*s3vectors.DeleteVectorsOutput, error)
	listVectors   func(*s3vectors.ListVectorsInput) (*s3vectors.ListVectorsOutput, error)
}

func (f *fakeAPI) GetIndex(_ context.Context, in *s3vectors.GetIndexInput, _ ...func(*s3vectors.Options)) (*s3vectors.GetIndexOutput, error) {
	return f.getIndex(in)
}

func (f *fakeAPI) CreateIndex(_ context.Context, in *s3vectors.CreateIndexInput, _ ...func(*s3vectors.Options)) (*s3vectors.CreateIndexOutput, error) {
	return f.createIndex(in)
}

func (f *fakeAPI) PutVectors(_ context.Context, in *s3vectors.PutVectorsInput, _ ...func(*s3vectors.Options)) (*s3vectors.PutVectorsOutput, error) {
	return f.putVectors(in)
}

func (f *fakeAPI) QueryVectors(_ context.Context, in *s3vectors.QueryVectorsInput, _ ...func(*s3vectors.Options)) (*s3vectors.QueryVectorsOutput, error) {
	return f.queryVectors(in)
}

func (f *fakeAPI) DeleteVectors(_ context.Context, in *s3vectors.DeleteVectorsInput, _ ...func(*s3vectors.Options)) (*s3vectors.DeleteVectorsOutput, error) {
	return f.deleteVectors(in)
}

func (f *fakeAPI) ListVectors(_ context.Context, in *s3vectors.ListVectorsInput, _ ...func(*s3vectors.Options)) (*s3vectors.ListVectorsOutput, error) {
	return f.listVectors(in)
}

func newStore(f *fakeAPI) *Store {
	return &Store{api: f, bucket: "b", index: "i", dim: 4, distance: types.DistanceMetricCosine}
}

func TestValidateIndexName(t *testing.T) {
	t.Parallel()
	valid := []string{"galdor-chunks", "galdor-chunks-t1", "abc", "a1.b2-c3", "skills.backend"}
	for _, n := range valid {
		if err := validateIndexName(n); err != nil {
			t.Errorf("validateIndexName(%q) = %v, want nil", n, err)
		}
	}
	invalid := []string{
		"galdor_chunks", // underscore (the real-world footgun)
		"Galdor-Chunks", // uppercase
		"ab",            // too short
		"-leading",      // must start alnum
		"trailing-",     // must end alnum
		"has space",     // invalid char
	}
	for _, n := range invalid {
		if err := validateIndexName(n); err == nil {
			t.Errorf("validateIndexName(%q) = nil, want error", n)
		}
	}
}

func TestBuildMetadata_RejectsReservedKeys(t *testing.T) {
	t.Parallel()
	_, err := buildMetadata(memory.Chunk{
		ID: "x", DocumentID: "d",
		Metadata: map[string]string{"__document_id": "spoofed"},
	})
	if err == nil {
		t.Fatal("a reserved __-prefixed metadata key must be rejected")
	}
}

func TestChunkFromVector_RoundTrip(t *testing.T) {
	t.Parallel()
	meta := document.NewLazyDocument(map[string]any{
		metaKeyDocumentID:    "doc1",
		metaKeyIndex:         3,
		metaKeyText:          "hello",
		"lang":               "es",
		"source":             "wiki",
		"ignored_non_string": 42,
	})
	c := chunkFromVector(aws.String("doc1#3"), meta)
	if c.ID != "doc1#3" || c.DocumentID != "doc1" || c.Index != 3 || c.Text != "hello" {
		t.Errorf("chunk fields = %+v", c)
	}
	if c.Metadata["lang"] != "es" || c.Metadata["source"] != "wiki" {
		t.Errorf("metadata = %+v", c.Metadata)
	}
	if _, has := c.Metadata["ignored_non_string"]; has {
		t.Error("non-string metadata value should not appear in Metadata")
	}
}

// filterJSON marshals a built filter the way the SDK serializes it onto
// the request, so tests can assert the exact wire shape.
func filterJSON(t *testing.T, filter map[string]string) map[string]any {
	t.Helper()
	f := buildFilter(filter)
	if f == nil {
		t.Fatal("expected a non-nil filter document")
	}
	b, err := f.MarshalSmithyDocument()
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBuildFilter_Empty(t *testing.T) {
	t.Parallel()
	if buildFilter(nil) != nil || buildFilter(map[string]string{}) != nil {
		t.Error("empty filter must produce a nil document")
	}
}

func TestBuildFilter_SingleKeyBare(t *testing.T) {
	t.Parallel()
	// One pair uses the implicit-$eq bare form {"k":"v"}.
	raw := filterJSON(t, map[string]string{"lang": "es"})
	if raw["lang"] != "es" {
		t.Errorf("single-key filter = %+v, want {lang:es}", raw)
	}
	if _, hasAnd := raw["$and"]; hasAnd {
		t.Error("single-key filter must not use $and")
	}
}

func TestBuildFilter_MultiKeyUsesAnd(t *testing.T) {
	t.Parallel()
	// S3 Vectors rejects implicit AND across top-level keys; two pairs must
	// be combined with an explicit $and array of single-key conditions.
	raw := filterJSON(t, map[string]string{"type": "skill", "category": "backend"})
	conds, ok := raw["$and"].([]any)
	if !ok {
		t.Fatalf("multi-key filter must use $and array, got %+v", raw)
	}
	if len(conds) != 2 {
		t.Fatalf("$and must hold 2 conditions, got %d: %+v", len(conds), conds)
	}
	// Collect the single-key conditions regardless of map order.
	got := map[string]any{}
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || len(m) != 1 {
			t.Fatalf("each $and condition must be a single-key object, got %#v", c)
		}
		maps.Copy(got, m)
	}
	if got["type"] != "skill" || got["category"] != "backend" {
		t.Errorf("$and conditions = %+v, want type=skill, category=backend", got)
	}
}

func TestEnsureIndex_CreatesWhenMissing(t *testing.T) {
	t.Parallel()
	var created *s3vectors.CreateIndexInput
	f := &fakeAPI{
		getIndex: func(*s3vectors.GetIndexInput) (*s3vectors.GetIndexOutput, error) {
			return nil, &types.NotFoundException{}
		},
		createIndex: func(in *s3vectors.CreateIndexInput) (*s3vectors.CreateIndexOutput, error) {
			created = in
			return &s3vectors.CreateIndexOutput{}, nil
		},
	}
	if err := newStore(f).ensureIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if created == nil {
		t.Fatal("expected CreateIndex to be called on a missing index")
	}
	if created.Dimension == nil || *created.Dimension != 4 {
		t.Errorf("dimension = %v, want 4", created.Dimension)
	}
	if created.DistanceMetric != types.DistanceMetricCosine {
		t.Errorf("distance = %v, want cosine", created.DistanceMetric)
	}
	if created.DataType != types.DataTypeFloat32 {
		t.Errorf("dataType = %v, want float32", created.DataType)
	}
	if created.MetadataConfiguration == nil ||
		len(created.MetadataConfiguration.NonFilterableMetadataKeys) != 1 ||
		created.MetadataConfiguration.NonFilterableMetadataKeys[0] != metaKeyText {
		t.Errorf("__text must be declared non-filterable: %+v", created.MetadataConfiguration)
	}
}

func TestEnsureIndex_NoCreateWhenExists(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{
		getIndex: func(*s3vectors.GetIndexInput) (*s3vectors.GetIndexOutput, error) {
			return &s3vectors.GetIndexOutput{Index: &types.Index{Dimension: aws.Int32(4)}}, nil
		},
		createIndex: func(*s3vectors.CreateIndexInput) (*s3vectors.CreateIndexOutput, error) {
			t.Fatal("CreateIndex must not be called when the index exists")
			return nil, nil
		},
	}
	if err := newStore(f).ensureIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureIndex_DimMismatch(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{
		getIndex: func(*s3vectors.GetIndexInput) (*s3vectors.GetIndexOutput, error) {
			return &s3vectors.GetIndexOutput{Index: &types.Index{Dimension: aws.Int32(8)}}, nil
		},
	}
	if err := newStore(f).ensureIndex(context.Background()); err == nil {
		t.Fatal("expected a dimension-mismatch error (index 8 vs config 4)")
	}
}

func TestAdd_ValidatesDimAndID(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{
		putVectors: func(*s3vectors.PutVectorsInput) (*s3vectors.PutVectorsOutput, error) {
			t.Fatal("PutVectors must not be called on invalid input")
			return nil, nil
		},
	}
	s := newStore(f)
	if err := s.Add(context.Background(), []memory.Chunk{{DocumentID: "d", Embedding: []float32{1, 0, 0, 0}}}); err == nil {
		t.Error("expected error for empty Chunk.ID")
	}
	if err := s.Add(context.Background(), []memory.Chunk{{ID: "x", Embedding: []float32{1, 0}}}); err == nil {
		t.Error("expected dim-mismatch error")
	}
}

func TestAdd_BatchesPuts(t *testing.T) {
	t.Parallel()
	var batches []int
	f := &fakeAPI{
		putVectors: func(in *s3vectors.PutVectorsInput) (*s3vectors.PutVectorsOutput, error) {
			batches = append(batches, len(in.Vectors))
			return &s3vectors.PutVectorsOutput{}, nil
		},
	}
	chunks := make([]memory.Chunk, 0, maxPutBatch*2+1)
	for i := range maxPutBatch*2 + 1 {
		chunks = append(chunks, memory.Chunk{ID: "c" + strconv.Itoa(i), Embedding: []float32{1, 0, 0, 0}})
	}
	if err := newStore(f).Add(context.Background(), chunks); err != nil {
		t.Fatal(err)
	}
	want := []int{maxPutBatch, maxPutBatch, 1}
	if len(batches) != len(want) {
		t.Fatalf("got %d batches %v, want %v", len(batches), batches, want)
	}
	for i := range want {
		if batches[i] != want[i] {
			t.Errorf("batch %d = %d, want %d", i, batches[i], want[i])
		}
	}
}

func TestRetrieve_RejectsEmptyEmbedding(t *testing.T) {
	t.Parallel()
	s := newStore(&fakeAPI{})
	if _, err := s.Retrieve(context.Background(), memory.Query{Text: "hi"}); err == nil {
		t.Fatal("expected error for a text-only query (vector-only backend)")
	}
}

func TestRetrieve_ConvertsDistanceAndDropsNegatives(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{
		queryVectors: func(in *s3vectors.QueryVectorsInput) (*s3vectors.QueryVectorsOutput, error) {
			if !in.ReturnDistance || !in.ReturnMetadata {
				t.Error("Retrieve must request distance and metadata")
			}
			return &s3vectors.QueryVectorsOutput{Vectors: []types.QueryOutputVector{
				{Key: aws.String("a"), Distance: aws.Float32(0.1), Metadata: document.NewLazyDocument(map[string]any{metaKeyText: "near"})},
				{Key: aws.String("b"), Distance: aws.Float32(1.8), Metadata: document.NewLazyDocument(map[string]any{metaKeyText: "anti"})},
			}}, nil
		},
	}
	res, err := newStore(f).Retrieve(context.Background(), memory.Query{Embedding: []float32{1, 0, 0, 0}, K: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1 (the anti-correlated hit must be dropped)", len(res))
	}
	if res[0].Chunk.ID != "a" || res[0].Chunk.Text != "near" {
		t.Errorf("chunk = %+v", res[0].Chunk)
	}
	if d := res[0].Score - 0.9; d > 1e-6 || d < -1e-6 {
		t.Errorf("score = %v, want 0.9 (1 - 0.1)", res[0].Score)
	}
}

func TestRetrieve_EuclideanScoresWithoutDrops(t *testing.T) {
	t.Parallel()
	// Euclidean distance is squared L2 in [0, +inf); the score map must be
	// 1/(1+dist) (monotone-decreasing, always positive) so far neighbors
	// are NOT dropped the way the cosine score<0 cutoff would drop them.
	f := &fakeAPI{
		queryVectors: func(*s3vectors.QueryVectorsInput) (*s3vectors.QueryVectorsOutput, error) {
			return &s3vectors.QueryVectorsOutput{
				DistanceMetric: types.DistanceMetricEuclidean,
				Vectors: []types.QueryOutputVector{
					{Key: aws.String("near"), Distance: aws.Float32(0), Metadata: document.NewLazyDocument(map[string]any{metaKeyText: "n"})},
					{Key: aws.String("far"), Distance: aws.Float32(9), Metadata: document.NewLazyDocument(map[string]any{metaKeyText: "f"})},
				},
			}, nil
		},
	}
	res, err := newStore(f).Retrieve(context.Background(), memory.Query{Embedding: []float32{1, 0, 0, 0}, K: 5})
	if err != nil {
		t.Fatal(err)
	}
	// Both kept (dist=9 would be a negative score under the cosine cutoff).
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2 (euclidean must not drop far neighbors)", len(res))
	}
	if d := res[0].Score - 1.0; d > 1e-6 || d < -1e-6 {
		t.Errorf("near score = %v, want 1.0 (1/(1+0))", res[0].Score)
	}
	if d := res[1].Score - 0.1; d > 1e-6 || d < -1e-6 {
		t.Errorf("far score = %v, want 0.1 (1/(1+9))", res[1].Score)
	}
	if res[0].Score <= res[1].Score {
		t.Error("euclidean score must be monotone-decreasing in distance (near > far)")
	}
}

func TestRetrieve_PaginatesUntilK(t *testing.T) {
	t.Parallel()
	// QueryVectors caps a page at 100 results; with K=150 Retrieve must
	// follow NextToken to assemble the full set.
	page := func(prefix string, n int) []types.QueryOutputVector {
		out := make([]types.QueryOutputVector, 0, n)
		for i := range n {
			out = append(out, types.QueryOutputVector{
				Key:      aws.String(prefix + strconv.Itoa(i)),
				Distance: aws.Float32(0.2),
				Metadata: document.NewLazyDocument(map[string]any{metaKeyText: "x"}),
			})
		}
		return out
	}
	calls := 0
	f := &fakeAPI{
		queryVectors: func(in *s3vectors.QueryVectorsInput) (*s3vectors.QueryVectorsOutput, error) {
			calls++
			if calls == 1 {
				if in.TopK == nil || *in.TopK != 150 {
					t.Errorf("TopK = %v, want 150", in.TopK)
				}
				return &s3vectors.QueryVectorsOutput{Vectors: page("p1-", 100), NextToken: aws.String("tok")}, nil
			}
			if in.NextToken == nil || *in.NextToken != "tok" {
				t.Errorf("second page must pass NextToken, got %v", in.NextToken)
			}
			return &s3vectors.QueryVectorsOutput{Vectors: page("p2-", 50)}, nil
		},
	}
	res, err := newStore(f).Retrieve(context.Background(), memory.Query{Embedding: []float32{1, 0, 0, 0}, K: 150})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 150 {
		t.Fatalf("got %d results, want 150 across two pages", len(res))
	}
	if calls != 2 {
		t.Errorf("QueryVectors calls = %d, want 2", calls)
	}
}

func TestDelete_EmptyDocumentID(t *testing.T) {
	t.Parallel()
	if err := newStore(&fakeAPI{}).Delete(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty documentID")
	}
}

func TestDelete_FiltersByDocumentIDAcrossPagesAndBatches(t *testing.T) {
	t.Parallel()
	// Two pages; only chunks of doc "d1" should be deleted.
	page1 := []types.ListOutputVector{
		{Key: aws.String("d1#0"), Metadata: document.NewLazyDocument(map[string]any{metaKeyDocumentID: "d1"})},
		{Key: aws.String("d2#0"), Metadata: document.NewLazyDocument(map[string]any{metaKeyDocumentID: "d2"})},
	}
	page2 := []types.ListOutputVector{
		{Key: aws.String("d1#1"), Metadata: document.NewLazyDocument(map[string]any{metaKeyDocumentID: "d1"})},
	}
	calls := 0
	var deleted []string
	f := &fakeAPI{
		listVectors: func(in *s3vectors.ListVectorsInput) (*s3vectors.ListVectorsOutput, error) {
			calls++
			if calls == 1 {
				return &s3vectors.ListVectorsOutput{Vectors: page1, NextToken: aws.String("tok")}, nil
			}
			if in.NextToken == nil || *in.NextToken != "tok" {
				t.Errorf("second page must pass the NextToken, got %v", in.NextToken)
			}
			return &s3vectors.ListVectorsOutput{Vectors: page2}, nil
		},
		deleteVectors: func(in *s3vectors.DeleteVectorsInput) (*s3vectors.DeleteVectorsOutput, error) {
			deleted = append(deleted, in.Keys...)
			return &s3vectors.DeleteVectorsOutput{}, nil
		},
	}
	if err := newStore(f).Delete(context.Background(), "d1"); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 || deleted[0] != "d1#0" || deleted[1] != "d1#1" {
		t.Errorf("deleted keys = %v, want [d1#0 d1#1]", deleted)
	}
}

func TestLen_CountsAcrossPages(t *testing.T) {
	t.Parallel()
	calls := 0
	f := &fakeAPI{
		listVectors: func(in *s3vectors.ListVectorsInput) (*s3vectors.ListVectorsOutput, error) {
			calls++
			if calls == 1 {
				return &s3vectors.ListVectorsOutput{
					Vectors:   []types.ListOutputVector{{Key: aws.String("a")}, {Key: aws.String("b")}},
					NextToken: aws.String("tok"),
				}, nil
			}
			return &s3vectors.ListVectorsOutput{Vectors: []types.ListOutputVector{{Key: aws.String("c")}}}, nil
		},
	}
	n, err := newStore(f).Len(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("Len = %d, want 3 (2 + 1 across two pages)", n)
	}
}

func TestDelete_NoMatchIsNoOp(t *testing.T) {
	t.Parallel()
	f := &fakeAPI{
		listVectors: func(*s3vectors.ListVectorsInput) (*s3vectors.ListVectorsOutput, error) {
			return &s3vectors.ListVectorsOutput{Vectors: []types.ListOutputVector{
				{Key: aws.String("d2#0"), Metadata: document.NewLazyDocument(map[string]any{metaKeyDocumentID: "d2"})},
			}}, nil
		},
		deleteVectors: func(*s3vectors.DeleteVectorsInput) (*s3vectors.DeleteVectorsOutput, error) {
			t.Fatal("DeleteVectors must not be called when nothing matches")
			return nil, nil
		},
	}
	if err := newStore(f).Delete(context.Background(), "missing"); err != nil {
		t.Fatal(err)
	}
}
