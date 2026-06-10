package a2a

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// sendParams builds a tasks/send rpcMessage for a given id.
func sendParams(t *testing.T, id, text string) rpcMessage {
	t.Helper()
	raw, err := json.Marshal(tasksSendParams{ID: id, Message: UserText(text)})
	if err != nil {
		t.Fatal(err)
	}
	return rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: MethodTasksSend, Params: raw}
}

// Regression for audit H17: the in-memory task store is bounded. Once at
// capacity, a new task evicts the oldest terminal task instead of growing
// without limit.
func TestTaskStore_BoundedByMaxTasks(t *testing.T) {
	srv := NewServer(AgentCard{Name: "t", Version: "1"}, HandlerFunc(
		func(_ context.Context, task *Task) error { task.Status.State = TaskCompleted; return nil }))
	srv.maxTasks = 3

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		reply := srv.handleTasksSend(ctx, sendParams(t, "task-"+string(rune('a'+i)), "hi"))
		if reply.Error != nil {
			t.Fatalf("send %d errored: %+v", i, reply.Error)
		}
	}
	srv.mu.Lock()
	n := len(srv.tasks)
	srv.mu.Unlock()
	if n > srv.maxTasks {
		t.Fatalf("store grew to %d, must stay within maxTasks=%d (regression of H17)", n, srv.maxTasks)
	}
}

// A client-supplied task ID past the length cap is rejected.
func TestTaskStore_RejectsOverlongID(t *testing.T) {
	srv := NewServer(AgentCard{Name: "t", Version: "1"}, nil)
	reply := srv.handleTasksSend(context.Background(), sendParams(t, strings.Repeat("z", maxTaskIDLen+1), "hi"))
	if reply.Error == nil {
		t.Fatal("expected an error for an over-long task ID (regression of H17)")
	}
	if reply.Error.Code != ErrCodeInvalidParams {
		t.Errorf("error code = %d, want ErrCodeInvalidParams", reply.Error.Code)
	}
}
