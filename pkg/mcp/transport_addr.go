package mcp

// SSETransportAddr returns the bound network address of a Transport
// constructed by NewSSETransport. The second return is false if t was
// not an SSE transport. Useful for tests that pass ":0" and need to
// learn the OS-assigned port.
func SSETransportAddr(t Transport) (string, bool) {
	s, ok := t.(*sseTransport)
	if !ok {
		return "", false
	}
	return s.Addr(), true
}

// StreamableHTTPTransportAddr returns the bound network address of a
// Transport constructed by NewStreamableHTTPTransport.
func StreamableHTTPTransportAddr(t Transport) (string, bool) {
	s, ok := t.(*streamableHTTPTransport)
	if !ok {
		return "", false
	}
	return s.Addr(), true
}
