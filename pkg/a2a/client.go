package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Client speaks A2A to a single remote agent. The zero value is not
// usable; call NewClient.
//
// The Client multiplexes requests over the configured HTTP client.
// JSON-RPC ids are unique per Client instance and auto-incremented.
type Client struct {
	baseURL string
	http    *http.Client
	id      atomic.Int64

	// AgentCardURL overrides the default discovery URL
	// (baseURL + AgentCardPath). Set this when the card is served
	// from a different host than the JSON-RPC endpoint.
	AgentCardURL string
}

// NewClient constructs a Client pointed at baseURL — the agent's
// JSON-RPC endpoint. The default http.Client has a 60s timeout;
// override with the Configure functional option.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// ClientOption configures NewClient.
type ClientOption func(*Client)

// WithHTTPClient overrides the default http.Client.
func WithHTTPClient(client *http.Client) ClientOption {
	return func(c *Client) {
		if client != nil {
			c.http = client
		}
	}
}

// FetchAgentCard hits the well-known path and returns the parsed
// Agent Card. The Client uses AgentCardURL if set; otherwise it
// derives the URL from baseURL.
func (c *Client) FetchAgentCard(ctx context.Context) (AgentCard, error) {
	url := c.AgentCardURL
	if url == "" {
		url = c.baseURL + AgentCardPath
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return AgentCard{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return AgentCard{}, fmt.Errorf("a2a: fetch agent card: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return AgentCard{}, fmt.Errorf("a2a: agent card HTTP %d: %s", resp.StatusCode, string(body))
	}
	var card AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		return AgentCard{}, fmt.Errorf("a2a: decode agent card: %w", err)
	}
	return card, nil
}

// SendTask posts a tasks/send request and returns the resulting Task.
// When taskID is empty the server allocates one; reuse the returned
// Task.ID on follow-up sends to continue a multi-turn conversation
// or to satisfy an input-required state.
func (c *Client) SendTask(ctx context.Context, message Message, opts ...SendOption) (*Task, error) {
	p := tasksSendParams{Message: message}
	for _, opt := range opts {
		opt(&p)
	}
	if len(p.Message.Parts) == 0 {
		return nil, errors.New("a2a: SendTask: message has no parts")
	}
	var out Task
	if err := c.call(ctx, MethodTasksSend, p, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SendOption configures SendTask.
type SendOption func(*tasksSendParams)

// WithTaskID continues a previously-created task (typical when the
// server transitioned to input-required and the user is replying).
func WithTaskID(id string) SendOption {
	return func(p *tasksSendParams) { p.ID = id }
}

// WithSessionID groups related tasks under one logical session.
func WithSessionID(id string) SendOption {
	return func(p *tasksSendParams) { p.SessionID = id }
}

// WithMetadata attaches metadata to the task on the server.
func WithMetadata(md map[string]any) SendOption {
	return func(p *tasksSendParams) { p.Metadata = md }
}

// GetTask fetches the current state of a task by ID.
//
// historyLength, when > 0, asks the server to truncate Task.History
// to the most-recent N messages. Use 0 to receive the full history.
func (c *Client) GetTask(ctx context.Context, id string, historyLength int) (*Task, error) {
	if id == "" {
		return nil, errors.New("a2a: GetTask: id is required")
	}
	var out Task
	if err := c.call(ctx, MethodTasksGet, tasksGetParams{ID: id, HistoryLength: historyLength}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// call issues a JSON-RPC request and decodes the reply.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	id := c.id.Add(1)
	idBytes, _ := json.Marshal(id)
	rawParams, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("a2a: encode %s params: %w", method, err)
	}
	body, err := json.Marshal(rpcMessage{
		JSONRPC: "2.0",
		ID:      idBytes,
		Method:  method,
		Params:  rawParams,
	})
	if err != nil {
		return fmt.Errorf("a2a: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("a2a: HTTP transport: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("a2a: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("a2a: HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var msg rpcMessage
	if err := json.Unmarshal(respBody, &msg); err != nil {
		return fmt.Errorf("a2a: decode response: %w", err)
	}
	if msg.Error != nil {
		return msg.Error
	}
	if out == nil || len(msg.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(msg.Result, out); err != nil {
		return fmt.Errorf("a2a: decode %s result: %w", method, err)
	}
	return nil
}
