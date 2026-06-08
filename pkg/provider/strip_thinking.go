package provider

import (
	"context"
	"regexp"
	"strings"

	"github.com/YasserCR/galdor/pkg/schema"
)

// thinkBlockRe matches a complete inline <think>...</think> or
// <thinking>...</thinking> block. Non-greedy so multiple blocks in
// the same string strip independently; case-insensitive and
// dot-matches-newline because models emit multi-line reasoning.
// Group 1 captures the inner reasoning text so ExtractThinkingBlocks
// can keep it; StripThinkingBlocks ignores the group and just deletes
// the whole match.
var thinkBlockRe = regexp.MustCompile(`(?is)<(?:think|thinking)\b[^>]*>(.*?)</(?:think|thinking)>`)

// openThinkRe matches the start of a thinking block; used by the
// streaming wrapper to detect when to start buffering. The closing
// tag may not have arrived yet, so this is purposely partial.
var openThinkRe = regexp.MustCompile(`(?is)<(think|thinking)\b[^>]*>`)

// StripThinkingBlocks wraps p so its responses have inline
// <think>...</think> and <thinking>...</thinking> blocks removed from
// any text content. Useful for OpenAI-compatible thinking models
// (MiniMax, DeepSeek, Qwen) that emit chain-of-thought inline in the
// text body when the caller forwards the answer to a downstream that
// can't tolerate it (Telegram HTML, a JSON-only contract, etc.).
//
// Matching is case-insensitive and non-greedy. Whitespace immediately
// after a stripped block is trimmed only when the strip actually
// changed the string, so passthrough text keeps its exact shape.
//
// This wrapper only handles inline <think> text markers. Structured
// reasoning parts (schema.ContentTypeThinking, e.g. Anthropic extended
// thinking or Gemini thoughts) are left untouched — they are surfaced
// at the provider layer, not here.
//
// To KEEP the reasoning instead of discarding it (capturing it as a
// schema.ContentTypeThinking part for observability), use
// ExtractThinkingBlocks.
//
// Streaming: the returned Provider's Stream wraps the underlying
// StreamReader. Once it sees a `<think>` open tag in a delta, it
// suppresses everything until the matching `</think>` (which may
// straddle deltas). A small lookahead buffer is kept across deltas
// to catch closing tags split between frames. If the stream ends
// (EventMessageStop or io.EOF) while the buffer still holds an open
// block, the buffer is dropped — an unclosed `<think>` is treated as
// "all remaining text was reasoning". Callers who want the partial
// reasoning surfaced should not install this middleware.
func StripThinkingBlocks(p Provider) Provider {
	if p == nil {
		panic("provider: StripThinkingBlocks inner cannot be nil")
	}
	return &stripThinkingProvider{inner: p}
}

// ExtractThinkingBlocks wraps p like StripThinkingBlocks but, instead
// of discarding the inline <think>...</think> reasoning, MOVES it into
// a separate schema.ContentTypeThinking part on the response message.
//
// The text parts end up identical to what StripThinkingBlocks would
// produce (reasoning removed), so Message.Text() — and every consumer
// that reads it — is unaffected; the reasoning is merely preserved as
// an extra, non-text part for observability or UIs to surface.
//
// Streaming: live content deltas are stripped exactly as
// StripThinkingBlocks does (so downstream stays clean), and on the
// terminal event the reasoning is moved into a thinking part IF the
// underlying provider populates the final Message. Providers that emit
// no final Message on stop (only deltas) cannot have their streamed
// reasoning captured this way — surfacing reasoning live is a separate
// concern (a dedicated stream event), out of scope here.
func ExtractThinkingBlocks(p Provider) Provider {
	if p == nil {
		panic("provider: ExtractThinkingBlocks inner cannot be nil")
	}
	return &stripThinkingProvider{inner: p, collect: true}
}

type stripThinkingProvider struct {
	inner Provider
	// collect moves reasoning into a thinking part instead of
	// discarding it (ExtractThinkingBlocks vs StripThinkingBlocks).
	collect bool
}

func (s *stripThinkingProvider) Name() string               { return s.inner.Name() }
func (s *stripThinkingProvider) Capabilities() Capabilities { return s.inner.Capabilities() }

func (s *stripThinkingProvider) Generate(ctx context.Context, req Request) (*Response, error) {
	resp, err := s.inner.Generate(ctx, req)
	if err != nil || resp == nil {
		return resp, err
	}
	if s.collect {
		extractMessage(&resp.Message)
	} else {
		stripMessage(&resp.Message)
	}
	return resp, nil
}

func (s *stripThinkingProvider) Stream(ctx context.Context, req Request) (StreamReader, error) {
	sr, err := s.inner.Stream(ctx, req)
	if err != nil {
		return sr, err
	}
	return &stripThinkingStream{inner: sr, collect: s.collect}, nil
}

// stripMessage rewrites every text part of m, dropping inline
// thinking blocks.
func stripMessage(m *schema.Message) {
	for i, p := range m.Content {
		if p.Type != schema.ContentTypeText {
			continue
		}
		if cleaned, changed := stripText(p.Text); changed {
			m.Content[i].Text = cleaned
		}
	}
}

// stripText returns the input with all complete think blocks removed.
// The second return reports whether the input was modified.
func stripText(in string) (string, bool) {
	if !strings.ContainsAny(in, "<") {
		return in, false
	}
	out := thinkBlockRe.ReplaceAllString(in, "")
	if out == in {
		return in, false
	}
	return strings.TrimSpace(out), true
}

// extractMessage rewrites every text part of m, moving the inline
// reasoning out of the text and appending it as separate thinking
// parts. The text parts are left exactly as stripMessage would leave
// them, so Message.Text() is unchanged.
func extractMessage(m *schema.Message) {
	var thinks []string
	for i, p := range m.Content {
		if p.Type != schema.ContentTypeText {
			continue
		}
		if cleaned, th, changed := extractText(p.Text); changed {
			m.Content[i].Text = cleaned
			thinks = append(thinks, th...)
		}
	}
	for _, t := range thinks {
		if t = strings.TrimSpace(t); t != "" {
			m.Content = append(m.Content, schema.ThinkingPart(t))
		}
	}
}

// extractText returns the input with all complete think blocks removed
// (like stripText) plus the reasoning text captured from each block.
// The third return reports whether the input was modified.
func extractText(in string) (string, []string, bool) {
	if !strings.ContainsAny(in, "<") {
		return in, nil, false
	}
	matches := thinkBlockRe.FindAllStringSubmatch(in, -1)
	if len(matches) == 0 {
		return in, nil, false
	}
	thinks := make([]string, 0, len(matches))
	for _, m := range matches {
		thinks = append(thinks, m[1]) // group 1 = inner reasoning
	}
	out := thinkBlockRe.ReplaceAllString(in, "")
	return strings.TrimSpace(out), thinks, true
}

// stripThinkingStream wraps a StreamReader and rewrites
// EventContentDelta payloads on the fly.
//
// Buffering rules:
//   - When no `<think>` is open, deltas are forwarded as soon as they
//     can't possibly contain the start of an open tag. To avoid
//     splitting a tag across emit boundaries we hold back the trailing
//     few bytes ("<", "<t", ..., "<thinking") until the next delta
//     either completes them into a tag or proves they aren't one.
//   - When a `<think>` is open, deltas are swallowed until `</think>`
//     (or `</thinking>`) appears; remaining text after the close is
//     re-fed through the same logic so multiple alternating blocks in
//     one stream all strip cleanly.
//   - On EventMessageStop, any pending pre-tag bytes are flushed
//     downstream verbatim; if a `<think>` is still open the buffer is
//     discarded.
type stripThinkingStream struct {
	inner StreamReader

	// collect, when true, also rewrites the terminal Message so its
	// reasoning is moved into a thinking part (ExtractThinkingBlocks).
	// It has no effect on the live delta stream, which is stripped
	// identically either way.
	collect bool

	// buf holds either (a) the tail of forwarded text that might be
	// the prefix of an opening tag, or (b) the accumulated text
	// inside an open think block while we hunt for its close.
	buf string

	// inThink is true while we're inside a <think>...</think> region.
	inThink bool

	// pending holds events we need to emit before the next Recv
	// (when a single inbound delta produces multiple outbound
	// events, e.g. a clean delta followed by the flushed pre-stop
	// remainder).
	pending []Event
}

// maxOpenTagLen is the longest possible prefix of an open think tag
// we might need to keep in the lookahead buffer: `<thinking ` plus
// some attribute bytes. We cap at a small constant so the buffer
// can't grow unboundedly on adversarial input that keeps almost-but-
// not-quite opening a tag.
const maxOpenTagLen = 16

func (s *stripThinkingStream) Recv(ctx context.Context) (Event, error) {
	if len(s.pending) > 0 {
		ev := s.pending[0]
		s.pending = s.pending[1:]
		return ev, nil
	}
	for {
		ev, err := s.inner.Recv(ctx)
		if err != nil {
			return ev, err
		}
		switch ev.Type {
		case EventContentDelta:
			out := s.feed(ev.ContentDelta)
			if out == "" {
				continue // fully buffered or fully suppressed
			}
			ev.ContentDelta = out
			return ev, nil
		case EventMessageStop:
			// In extract mode, move the reasoning out of the terminal
			// message's text into a thinking part (when the provider
			// populates a final Message). Copy the Content slice so we
			// never mutate the provider's own data.
			if s.collect && ev.Message != nil {
				cp := *ev.Message
				cp.Content = append([]schema.ContentPart(nil), ev.Message.Content...)
				extractMessage(&cp)
				ev.Message = &cp
			}
			// Flush whatever the buffer still holds. If we were
			// outside a think block, the bytes were just a non-tag
			// lookahead and must be emitted. If we were inside, the
			// model never closed the tag — drop the buffer.
			if !s.inThink && s.buf != "" {
				flush := s.buf
				s.buf = ""
				s.pending = append(s.pending, ev)
				return Event{Type: EventContentDelta, ContentDelta: flush}, nil
			}
			s.buf = ""
			s.inThink = false
			return ev, nil
		default:
			return ev, nil
		}
	}
}

func (s *stripThinkingStream) Close() error { return s.inner.Close() }

// feed processes a new chunk and returns the bytes that should be
// emitted as the EventContentDelta payload (possibly empty).
func (s *stripThinkingStream) feed(chunk string) string {
	work := s.buf + chunk
	s.buf = ""

	var out strings.Builder
	for {
		if s.inThink {
			// Look for the closing tag (any of </think> or </thinking>).
			idx, end := findClose(work)
			if idx < 0 {
				// No close yet; everything remains swallowed.
				// Don't accumulate — we don't need the suppressed
				// text. Keep a small tail in case the close tag is
				// split across this delta and the next.
				if n := len(work); n > 0 {
					tail := work
					if n > maxOpenTagLen {
						tail = work[n-maxOpenTagLen:]
					}
					s.buf = tail
				}
				return strings.TrimLeftFunc(out.String(), isSpace)
			}
			// Close found; resume normal scanning after it.
			work = work[end:]
			s.inThink = false
			// Trim a single run of whitespace right after the close,
			// matching the non-streaming strip's "trim if changed"
			// behavior at the seam.
			work = strings.TrimLeft(work, " \t\n\r")
		}
		// Outside a think block: look for an open tag.
		loc := openThinkRe.FindStringIndex(work)
		if loc == nil {
			// No open tag in the current buffer. Forward all but
			// the trailing slice that could be the start of one.
			safe, tail := splitSafePrefix(work)
			out.WriteString(safe)
			s.buf = tail
			return out.String()
		}
		// Emit text before the open tag, then enter think mode.
		out.WriteString(work[:loc[0]])
		work = work[loc[1]:]
		s.inThink = true
	}
}

// findClose locates a closing think tag in s. Returns the start and
// end positions of the tag, or (-1, -1) if not present.
func findClose(s string) (int, int) {
	// Cheap scan — closing tags are short and fixed (modulo case).
	lower := strings.ToLower(s)
	candidates := []string{"</think>", "</thinking>"}
	bestStart, bestEnd := -1, -1
	for _, c := range candidates {
		if i := strings.Index(lower, c); i >= 0 {
			if bestStart < 0 || i < bestStart {
				bestStart = i
				bestEnd = i + len(c)
			}
		}
	}
	return bestStart, bestEnd
}

// splitSafePrefix returns (safe, tail) where safe is the portion of
// in that's known not to be the start of an open think tag and tail
// is the trailing slice we must hold back until more bytes arrive.
// The tail is at most maxOpenTagLen bytes.
func splitSafePrefix(in string) (string, string) {
	// Find the last '<' in the input; everything before it is safe.
	// If there is no '<', the whole input is safe.
	idx := strings.LastIndexByte(in, '<')
	if idx < 0 {
		return in, ""
	}
	tail := in[idx:]
	// If the tail can't possibly be the prefix of <think or <thinking
	// (case-insensitive), it's safe to emit. Cheap check: compare
	// against the lowercased prefixes of the two tags.
	lt := strings.ToLower(tail)
	const t1 = "<think"
	const t2 = "<thinking"
	if !strings.HasPrefix(t1, lt) && !strings.HasPrefix(t2, lt) &&
		!strings.HasPrefix(lt, t1) {
		// Definitely not the start of a thinking tag prefix; emit all.
		return in, ""
	}
	// Cap held-back tail length defensively.
	if len(tail) > maxOpenTagLen {
		// The tail is long enough that if it were going to be a
		// thinking tag, the regex would have matched it already.
		return in, ""
	}
	return in[:idx], tail
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

// Compile-time interface assertions.
var (
	_ Provider     = (*stripThinkingProvider)(nil)
	_ StreamReader = (*stripThinkingStream)(nil)
)
