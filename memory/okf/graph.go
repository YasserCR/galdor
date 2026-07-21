package okf

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/YasserCR/galdor/pkg/memory"
)

// The link graph (§5.3). OKF cross-links are directed edges of an untyped
// relationship graph. buildConcepts already resolves each concept's links
// into the MetaOutlinks metadata; the Bundle turns that into a navigable
// graph (Outlinks/Inlinks/Neighborhood) and GraphExpander turns it into a
// retrieval-time expansion.

// buildGraph derives the forward and reverse adjacency maps from the
// concepts' resolved outlinks. It runs once per Bundle.
func (b *Bundle) buildGraph() {
	b.graphOnce.Do(func() {
		b.outAdj = make(map[string][]string, len(b.Concepts))
		b.inAdj = make(map[string][]string)
		for _, c := range b.Concepts {
			id := c.Metadata[MetaConceptID]
			outs := splitCSV(c.Metadata[MetaOutlinks])
			b.outAdj[id] = outs
			for _, o := range outs {
				b.inAdj[o] = append(b.inAdj[o], id)
			}
		}
	})
}

// Outlinks returns the concept ids that conceptID links to (forward edges),
// in document order. The result is a fresh slice; mutating it is safe.
func (b *Bundle) Outlinks(conceptID string) []string {
	b.buildGraph()
	return append([]string(nil), b.outAdj[conceptID]...)
}

// Inlinks returns the concept ids that link to conceptID (reverse edges),
// sorted for determinism. The result is a fresh slice.
func (b *Bundle) Inlinks(conceptID string) []string {
	b.buildGraph()
	in := append([]string(nil), b.inAdj[conceptID]...)
	sort.Strings(in)
	return in
}

// Neighborhood returns every concept reachable from conceptID within depth
// forward hops (depth 1 = direct outlinks, depth 2 also their outlinks, …),
// in breadth-first discovery order and excluding the seed itself. depth <= 0
// returns nil.
func (b *Bundle) Neighborhood(conceptID string, depth int) []string {
	if depth <= 0 {
		return nil
	}
	b.buildGraph()
	seen := map[string]bool{conceptID: true}
	var out []string
	frontier := []string{conceptID}
	for d := 0; d < depth && len(frontier) > 0; d++ {
		var next []string
		for _, id := range frontier {
			for _, nb := range b.outAdj[id] {
				if seen[nb] {
					continue
				}
				seen[nb] = true
				out = append(out, nb)
				next = append(next, nb)
			}
		}
		frontier = next
	}
	return out
}

// GraphExpander wraps a memory.Store and, after each Retrieve, appends the
// graph neighbors of the top hits — so "give me this concept and the
// concepts it links to" needs no manual outlink walking. It is opt-in and
// leaves the wrapped store's own ranking untouched: base hits come first in
// their original order, then the expanded neighbors with a decayed score.
//
// GraphExpander implements memory.Store; Add/Delete/Close delegate to Inner.
// It expands using the static bundle graph, so Inner and Bundle are assumed
// to describe the same concepts.
type GraphExpander struct {
	// Inner is the wrapped store (typically an *okf.Store).
	Inner memory.Store
	// Bundle supplies the link graph and the neighbor concepts' text.
	Bundle *Bundle
	// Depth is how many hops to expand (default 1 when <= 0).
	Depth int
	// Decay multiplies a neighbor's score per hop from its seed hit
	// (default 0.5 when <= 0). A depth-2 neighbor scores seed*Decay*Decay.
	Decay float32
	// IncludeInlinks also expands along reverse edges (concepts that link
	// to a hit), not just forward outlinks.
	IncludeInlinks bool

	once sync.Once
	byID map[string]memory.Chunk
}

// buildChunkIndex maps each concept id to a representative chunk (its first)
// so expanded neighbors can be returned as real memory.Results.
func (g *GraphExpander) buildChunkIndex() {
	g.once.Do(func() {
		g.byID = make(map[string]memory.Chunk)
		if g.Bundle == nil {
			return
		}
		for _, ch := range ChunkConcepts(g.Bundle.Concepts) {
			if _, ok := g.byID[ch.DocumentID]; !ok {
				g.byID[ch.DocumentID] = ch
			}
		}
	})
}

// Add implements memory.Store.
func (g *GraphExpander) Add(ctx context.Context, chunks []memory.Chunk) error {
	return g.Inner.Add(ctx, chunks)
}

// Delete implements memory.Store.
func (g *GraphExpander) Delete(ctx context.Context, documentID string) error {
	return g.Inner.Delete(ctx, documentID)
}

// Close implements memory.Store.
func (g *GraphExpander) Close() error { return g.Inner.Close() }

// Retrieve runs the query against Inner, then appends the graph
// neighborhood of each hit. Neighbors already present in the base results
// are skipped; a neighbor reached from several hits keeps its highest score.
// Retrieve implements memory.Store.
func (g *GraphExpander) Retrieve(ctx context.Context, q memory.Query) ([]memory.Result, error) {
	base, err := g.Inner.Retrieve(ctx, q)
	if err != nil || g.Bundle == nil || len(base) == 0 {
		return base, err
	}
	g.buildChunkIndex()

	depth := g.Depth
	if depth <= 0 {
		depth = 1
	}
	decay := g.Decay
	if decay <= 0 {
		decay = 0.5
	}

	// Concepts already surfaced by the base ranking: never duplicate them,
	// and never let an expansion outrank the real hit.
	inBase := make(map[string]bool, len(base))
	for _, r := range base {
		inBase[r.Chunk.Metadata[MetaConceptID]] = true
	}

	bestScore := make(map[string]float32)
	for _, r := range base {
		seed := r.Chunk.Metadata[MetaConceptID]
		for _, nb := range g.expand(seed, depth) {
			if inBase[nb.id] {
				continue
			}
			s := r.Score * float32(math.Pow(float64(decay), float64(nb.hops)))
			if cur, ok := bestScore[nb.id]; !ok || s > cur {
				bestScore[nb.id] = s
			}
		}
	}

	out := append([]memory.Result(nil), base...)
	// Deterministic append order: by descending score, then concept id.
	ids := make([]string, 0, len(bestScore))
	for id := range bestScore {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if bestScore[ids[i]] != bestScore[ids[j]] {
			return bestScore[ids[i]] > bestScore[ids[j]]
		}
		return ids[i] < ids[j]
	})
	for _, id := range ids {
		ch, ok := g.byID[id]
		if !ok {
			continue // neighbor not in the indexed bundle; skip
		}
		out = append(out, memory.Result{Chunk: ch, Score: bestScore[id]})
	}
	return out, nil
}

// neighborHop is a concept id together with its hop distance from a seed.
type neighborHop struct {
	id   string
	hops int
}

// expand does a breadth-first walk from seed up to depth hops, following
// outlinks and (when IncludeInlinks) inlinks, returning each concept with
// the fewest hops at which it was reached.
func (g *GraphExpander) expand(seed string, depth int) []neighborHop {
	b := g.Bundle
	b.buildGraph()
	seen := map[string]bool{seed: true}
	var out []neighborHop
	frontier := []string{seed}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, id := range frontier {
			adj := b.outAdj[id]
			if g.IncludeInlinks {
				adj = append(append([]string(nil), adj...), b.inAdj[id]...)
			}
			for _, nb := range adj {
				if seen[nb] {
					continue
				}
				seen[nb] = true
				out = append(out, neighborHop{id: nb, hops: d})
				next = append(next, nb)
			}
		}
		frontier = next
	}
	return out
}

// splitCSV splits a comma-joined metadata value, dropping empty fields.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
