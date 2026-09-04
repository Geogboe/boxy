package agentsdk

import (
	"context"
	"testing"
	"time"
)

func TestRemoteAgentRequestLogsSendsBoundedRequest(t *testing.T) {
	stream := newFakeServerStream()
	agent := NewRemoteAgent(AgentInfo{ID: "agent-a"}, stream)
	since := time.Date(2026, time.September, 3, 18, 30, 0, 0, time.UTC)

	requestID, err := agent.RequestLogs(context.Background(), since, 25)
	if err != nil {
		t.Fatalf("RequestLogs: %v", err)
	}
	msg := <-stream.sentCh
	request := msg.GetLogRequest()
	if request == nil {
		t.Fatalf("request = %#v, want log request", msg)
	}
	if request.GetRequestId() != requestID || requestID == "" {
		t.Fatalf("request id = %q, returned id = %q", request.GetRequestId(), requestID)
	}
	if request.GetSinceUnixNano() != since.UnixNano() || request.GetLimit() != 25 {
		t.Fatalf("request = %#v, want since=%d limit=25", request, since.UnixNano())
	}
}
