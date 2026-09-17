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

// TestNormalizeReplyBodies_KeepsEveryEvent guards the boundary between
// events: each is one payload, in order, never concatenated into a body
// that parses as nothing — and never cut to the first, because a server
// may send a notification ahead of the reply on the same stream.
func TestNormalizeReplyBodies_KeepsEveryEvent(t *testing.T) {
	t.Parallel()

	const body = "event: message\ndata: {\"method\":\"notifications/message\"}\n\n" +
		"event: message\ndata: {\"id\":1}\n\n"

	got := normalizeReplyBodies([]byte(body))
	if len(got) != 2 || string(got[0]) != `{"method":"notifications/message"}` || string(got[1]) != `{"id":1}` {
		t.Fatalf("normalizeReplyBodies = %q, want both events in order", got)
	}
	if first := string(normalizeReplyBody([]byte(body))); first != `{"method":"notifications/message"}` {
		t.Fatalf("normalizeReplyBody = %q, want the first event", first)
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
