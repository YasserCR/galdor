package graph

import (
	"fmt"
	"io"
	"sort"
)

// Spec is the introspected, JSON-serializable description of a
// compiled Runnable[S]. It is produced by Runnable.Inspect and is
// the input to renderers and validators that need to reason about
// graph topology without actually running it.
//
// The state type S is intentionally erased: a Spec describes shape,
// not behavior. The same Spec can be rendered to SVG, dumped to
// stdout, compared across versions in a regression test, or shipped
// to the embedded web UI for visualization.
type Spec struct {
	// Entry is the name of the first node executed after START.
	Entry string `json:"entry"`

	// Nodes lists every registered node, alphabetically sorted so
	// the spec is stable across runs.
	Nodes []NodeSpec `json:"nodes"`

	// StaticEdges are deterministic transitions: From → To. The
	// special edge START → Entry is included so renderers can place
	// the entry node visually.
	StaticEdges []EdgeSpec `json:"static_edges"`

	// ConditionalEdges are router-driven transitions. The target is
	// dynamic — chosen by the Router function at runtime — so the
	// Spec only records the source. Renderers typically draw a
	// dashed arrow with a "router" label fanning out of From.
	ConditionalEdges []EdgeSpec `json:"conditional_edges"`
}

// NodeSpec is the description of a single registered node.
type NodeSpec struct {
	Name string `json:"name"`

	// Interrupt reports whether the node is gated by
	// Graph.InterruptBefore — the runtime pauses before executing it.
	Interrupt bool `json:"interrupt,omitempty"`
}

// EdgeSpec is one transition. For static edges both From and To are
// set; for conditional edges only From is set.
type EdgeSpec struct {
	From string `json:"from"`
	To   string `json:"to,omitempty"`
}

// Inspect returns a Spec describing this Runnable's topology. The
// returned value owns its own slices and maps and is safe to mutate
// or marshal independently of the Runnable.
func (r *Runnable[S]) Inspect() Spec {
	spec := Spec{Entry: r.entry}

	// Nodes (alphabetical for stable output).
	names := make([]string, 0, len(r.nodes))
	for name := range r.nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	spec.Nodes = make([]NodeSpec, 0, len(names))
	for _, name := range names {
		_, interrupt := r.interruptBefore[name]
		spec.Nodes = append(spec.Nodes, NodeSpec{Name: name, Interrupt: interrupt})
	}

	// Static edges, including the START → Entry edge.
	staticSources := make([]string, 0, len(r.staticEdges))
	for from := range r.staticEdges {
		staticSources = append(staticSources, from)
	}
	sort.Strings(staticSources)
	spec.StaticEdges = make([]EdgeSpec, 0, len(staticSources))
	for _, from := range staticSources {
		spec.StaticEdges = append(spec.StaticEdges, EdgeSpec{From: from, To: r.staticEdges[from]})
	}

	// Conditional edges (target is dynamic).
	condSources := make([]string, 0, len(r.conditionalEdges))
	for from := range r.conditionalEdges {
		condSources = append(condSources, from)
	}
	sort.Strings(condSources)
	spec.ConditionalEdges = make([]EdgeSpec, 0, len(condSources))
	for _, from := range condSources {
		spec.ConditionalEdges = append(spec.ConditionalEdges, EdgeSpec{From: from})
	}

	return spec
}

// RenderSVG writes a self-contained SVG visualization of the spec to
// w. The layout is a layered (Sugiyama-style) flow from START on the
// left to END on the right; static edges are solid, conditional
// edges are dashed and labeled "router?".
//
// The SVG is fully self-contained (no external CSS, no JS) so it can
// be inlined into HTML, served as image/svg+xml, or piped through
// `rsvg-convert` to produce PNGs.
func (s Spec) RenderSVG(w io.Writer) error {
	layers := layeredPositions(s)
	maxRow := 0
	for _, l := range layers {
		if len(l) > maxRow {
			maxRow = len(l)
		}
	}

	const (
		nodeW     = 130
		nodeH     = 38
		gapX      = 60
		gapY      = 24
		padding   = 24
		fontFamly = "ui-sans-serif, system-ui, sans-serif"
	)

	cols := len(layers)
	widthPx := padding*2 + cols*nodeW + (cols-1)*gapX
	if widthPx < 360 {
		widthPx = 360
	}
	heightPx := padding*2 + maxRow*nodeH + (maxRow-1)*gapY
	if heightPx < 160 {
		heightPx = 160
	}

	// Compute node coordinates.
	pos := map[string]struct{ X, Y int }{}
	for col, layer := range layers {
		x := padding + col*(nodeW+gapX)
		// Vertically center this column's rows in the SVG.
		total := len(layer)*nodeH + (len(layer)-1)*gapY
		y0 := (heightPx - total) / 2
		for row, name := range layer {
			pos[name] = struct{ X, Y int }{X: x, Y: y0 + row*(nodeH+gapY)}
		}
	}

	if _, err := fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" font-family="%s" font-size="12">`,
		widthPx, heightPx, widthPx, heightPx, fontFamly); err != nil {
		return err
	}

	// Defs: arrowhead markers.
	if _, err := io.WriteString(w, `<defs>`+
		`<marker id="a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto"><path d="M0,0 L10,5 L0,10 z" fill="#475569"/></marker>`+
		`<marker id="ad" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto"><path d="M0,0 L10,5 L0,10 z" fill="#94a3b8"/></marker>`+
		`</defs>`); err != nil {
		return err
	}

	// Edges first so nodes paint on top.
	for _, e := range s.StaticEdges {
		drawEdge(w, pos, e.From, e.To, false, "")
	}
	for _, e := range s.ConditionalEdges {
		// Conditional edges have no static target; draw a short
		// dashed stub to the right of the source labeled "router".
		drawConditionalStub(w, pos, e.From)
	}

	// Nodes.
	for _, node := range s.Nodes {
		p, ok := pos[node.Name]
		if !ok {
			continue
		}
		fill := "#f1f5f9"
		stroke := "#475569"
		if node.Interrupt {
			fill = "#fef3c7"
			stroke = "#b45309"
		}
		fmt.Fprintf(w, `<rect x="%d" y="%d" width="%d" height="%d" rx="6" ry="6" fill="%q" stroke="%q" stroke-width="1.5"/>`,
			p.X, p.Y, nodeW, nodeH, fill, stroke)
		fmt.Fprintf(w, `<text x="%d" y="%d" text-anchor="middle" fill="#0f172a">%s</text>`,
			p.X+nodeW/2, p.Y+nodeH/2+4, escapeSVG(prettyName(node.Name)))
	}

	// START / END markers (drawn as smaller pill-shaped nodes).
	if p, ok := pos[START]; ok {
		fmt.Fprintf(w, `<rect x="%d" y="%d" width="%d" height="%d" rx="14" ry="14" fill="#10b981" stroke="#047857" stroke-width="1.5"/>`,
			p.X, p.Y, nodeW, nodeH)
		fmt.Fprintf(w, `<text x="%d" y="%d" text-anchor="middle" fill="white" font-weight="600">START</text>`,
			p.X+nodeW/2, p.Y+nodeH/2+4)
	}
	if p, ok := pos[END]; ok {
		fmt.Fprintf(w, `<rect x="%d" y="%d" width="%d" height="%d" rx="14" ry="14" fill="#1e293b" stroke="#0f172a" stroke-width="1.5"/>`,
			p.X, p.Y, nodeW, nodeH)
		fmt.Fprintf(w, `<text x="%d" y="%d" text-anchor="middle" fill="white" font-weight="600">END</text>`,
			p.X+nodeW/2, p.Y+nodeH/2+4)
	}

	if _, err := io.WriteString(w, `</svg>`); err != nil {
		return err
	}
	return nil
}

func drawEdge(w io.Writer, pos map[string]struct{ X, Y int }, from, to string, dashed bool, label string) {
	p1, ok1 := pos[from]
	p2, ok2 := pos[to]
	if !ok1 || !ok2 {
		return
	}
	const nodeW, nodeH = 130, 38
	x1 := p1.X + nodeW
	y1 := p1.Y + nodeH/2
	x2 := p2.X
	y2 := p2.Y + nodeH/2
	// Bezier curve so edges that span multiple rows don't go through
	// other nodes (we don't do collision detection, but a curve
	// reads better than a straight line).
	midX := (x1 + x2) / 2
	d := fmt.Sprintf("M%d,%d C%d,%d %d,%d %d,%d", x1, y1, midX, y1, midX, y2, x2, y2)
	dash := ""
	stroke := "#475569"
	marker := "url(#a)"
	if dashed {
		dash = ` stroke-dasharray="4 3"`
		stroke = "#94a3b8"
		marker = "url(#ad)"
	}
	fmt.Fprintf(w, `<path d=%q fill="none" stroke=%q stroke-width="1.5" marker-end="%s"%s/>`,
		d, stroke, marker, dash)
	if label != "" {
		fmt.Fprintf(w, `<text x="%d" y="%d" text-anchor="middle" fill="#64748b">%s</text>`,
			midX, (y1+y2)/2-4, escapeSVG(label))
	}
}

func drawConditionalStub(w io.Writer, pos map[string]struct{ X, Y int }, from string) {
	p, ok := pos[from]
	if !ok {
		return
	}
	const nodeW, nodeH = 130, 38
	x1 := p.X + nodeW
	y1 := p.Y + nodeH/2
	x2 := x1 + 32
	fmt.Fprintf(w, `<path d="M%d,%d L%d,%d" fill="none" stroke="#94a3b8" stroke-width="1.5" stroke-dasharray="4 3" marker-end="url(#ad)"/>`,
		x1, y1, x2, y1)
	fmt.Fprintf(w, `<text x="%d" y="%d" fill="#64748b">router?</text>`,
		x2+4, y1+4)
}

// layeredPositions assigns each node (including START and END) to a
// horizontal layer. Layer 0 is START. Subsequent layers are computed
// by BFS over static edges; conditional edges are followed but never
// extend the layer count (since their targets are dynamic). The
// returned outer slice is indexed by layer; the inner slice lists
// node names in that layer in registration order.
func layeredPositions(s Spec) [][]string {
	// Build adjacency from static edges.
	adj := map[string][]string{}
	for _, e := range s.StaticEdges {
		adj[e.From] = append(adj[e.From], e.To)
	}

	depth := map[string]int{START: 0}
	queue := []string{START}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			d := depth[cur] + 1
			if existing, seen := depth[next]; !seen || d > existing {
				depth[next] = d
				queue = append(queue, next)
			}
		}
	}

	// Any unreached nodes (e.g., orphan or reached only via routers)
	// land in a special "limbo" layer between START and END so they
	// at least appear. We place them in layer 1 by default.
	allNodes := []string{START, END}
	for _, n := range s.Nodes {
		allNodes = append(allNodes, n.Name)
	}
	for _, n := range allNodes {
		if _, ok := depth[n]; !ok {
			depth[n] = 1
		}
	}

	// Ensure END is at least as deep as the deepest non-END node.
	maxD := 0
	for n, d := range depth {
		if n == END {
			continue
		}
		if d > maxD {
			maxD = d
		}
	}
	if depth[END] <= maxD {
		depth[END] = maxD + 1
	}

	// Bucket into layers.
	layers := make([][]string, depth[END]+1)
	for _, n := range allNodes {
		d := depth[n]
		if d >= len(layers) {
			d = len(layers) - 1
		}
		layers[d] = append(layers[d], n)
	}
	// Sort each layer for deterministic output (START / END have
	// fixed positions naturally; the rest sort alphabetically).
	for i := range layers {
		sort.Strings(layers[i])
	}
	return layers
}

// escapeSVG escapes the four characters that SVG/XML treats
// specially in text content.
func escapeSVG(s string) string {
	r := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			r = append(r, '&', 'l', 't', ';')
		case '>':
			r = append(r, '&', 'g', 't', ';')
		case '&':
			r = append(r, '&', 'a', 'm', 'p', ';')
		case '"':
			r = append(r, '&', 'q', 'u', 'o', 't', ';')
		default:
			r = append(r, s[i])
		}
	}
	return string(r)
}

// prettyName trims the reserved sentinel underscores for display so
// "__start__" reads as "START" inside the SVG.
func prettyName(name string) string {
	switch name {
	case START:
		return "START"
	case END:
		return "END"
	}
	return name
}
