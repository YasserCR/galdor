package mcp

import "testing"

// TestNormalizeReplyBody covers the shapes a Streamable HTTP server may
// answer a POST with. The case that matters is the frame that opens with
// an `event:` line: the unwrapping used to key off a `data:` prefix on
// the body, so such a reply fell through unchanged, failed to parse as
// JSON and reached the caller as a connection that never answered.
func TestNormalizeReplyBody(t *testing.T) {
	t.Parallel()

	const payload = `{"jsonrpc":"2.0","id":1,"result":{}}`

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare json passes through",
			in:   payload,
			want: payload,
		},
		{
			name: "batch array passes through",
			in:   "[" + payload + "]",
			want: "[" + payload + "]",
		},
		{
			name: "frame opening with data",
			in:   "data: " + payload + "\n\n",
			want: payload,
		},
		{
			name: "frame opening with event",
			in:   "event: message\r\ndata: " + payload + "\r\n\r\n",
			want: payload,
		},
		{
			name: "frame with id and retry lines",
			in:   "id: 7\nretry: 3000\nevent: message\ndata: " + payload + "\n\n",
			want: payload,
		},
		{
			name: "comment only frame",
			in:   ": keep-alive\n\n",
			want: ": keep-alive",
		},
		{
			name: "empty body",
			in:   "",
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := string(normalizeReplyBody([]byte(tc.in))); got != tc.want {
				t.Fatalf("normalizeReplyBody(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizeReplyBody_StopsAtTheFirstEvent guards the boundary between
// events. A POST is answered with one reply; concatenating the data of
// every event in the body would hand the dispatch loop something that
// parses as nothing.
func TestNormalizeReplyBody_StopsAtTheFirstEvent(t *testing.T) {
	t.Parallel()

	const body = "event: message\ndata: {\"id\":1}\n\n" +
		"event: message\ndata: {\"id\":2}\n\n"

	if got := string(normalizeReplyBody([]byte(body))); got != `{"id":1}` {
		t.Fatalf("normalizeReplyBody = %q, want the first event only", got)
	}
}

// TestNormalizeReplyBody_JoinsDataLines follows the SSE rule that the
// successive data lines of one event are joined with a newline. A server
// that pretty-prints its JSON across lines still has to parse.
func TestNormalizeReplyBody_JoinsDataLines(t *testing.T) {
	t.Parallel()

	const body = "event: message\ndata: {\ndata: \"id\": 1\ndata: }\n\n"
	const want = "{\n\"id\": 1\n}"

	if got := string(normalizeReplyBody([]byte(body))); got != want {
		t.Fatalf("normalizeReplyBody = %q, want %q", got, want)
	}
}
