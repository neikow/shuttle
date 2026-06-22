package agent

import (
	"errors"
	"sync"
	"testing"

	shuttlev1 "github.com/neikow/shuttle/gen/shuttle/v1"
)

// recordSink captures the AgentEvents a producer sends, for assertions.
type recordSink struct {
	mu     sync.Mutex
	events []*shuttlev1.AgentEvent
}

func (r *recordSink) Send(ev *shuttlev1.AgentEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
	return nil
}

func (r *recordSink) logChunks() []*shuttlev1.DeployLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*shuttlev1.DeployLog
	for _, ev := range r.events {
		if dl, ok := ev.Payload.(*shuttlev1.AgentEvent_DeployLog); ok {
			out = append(out, dl.DeployLog)
		}
	}
	return out
}

func (r *recordSink) result() *shuttlev1.DeployResponse {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range r.events {
		if dr, ok := ev.Payload.(*shuttlev1.AgentEvent_DeployResult); ok {
			return dr.DeployResult
		}
	}
	return nil
}

// feed returns a closed channel preloaded with the given lines.
func feed(lines []LogLine) <-chan LogLine {
	ch := make(chan LogLine, len(lines))
	for _, l := range lines {
		ch <- l
	}
	close(ch)
	return ch
}

func TestStreamDeployResult_streamsChunksAndTerminal(t *testing.T) {
	var lines []LogLine
	for i := 0; i < 40; i++ {
		lines = append(lines, LogLine{Stream: "stdout", Text: "line"})
	}
	sink := &recordSink{}
	if err := streamDeployResult(sink, "dep-1", "svc", feed(lines), nil); err != nil {
		t.Fatalf("streamDeployResult: %v", err)
	}

	// Live chunks: every line is forwarded, tagged with deploy id + service.
	chunks := sink.logChunks()
	if len(chunks) == 0 {
		t.Fatal("expected at least one live DeployLog chunk")
	}
	streamed := 0
	for _, c := range chunks {
		if c.DeployId != "dep-1" || c.Service != "svc" {
			t.Errorf("chunk = %+v, want deploy_id=dep-1 service=svc", c)
		}
		streamed += len(c.Lines)
	}
	if streamed != len(lines) {
		t.Errorf("streamed %d lines live, want %d", streamed, len(lines))
	}
	// >32 lines forces an early batch flush, so it can't all arrive in one chunk.
	if len(chunks) < 2 {
		t.Errorf("expected the 40-line run to flush in >1 chunk, got %d", len(chunks))
	}

	// Terminal result still carries the complete logs and a SUCCESS status.
	res := sink.result()
	if res == nil {
		t.Fatal("expected a terminal DeployResponse")
	}
	if res.Status != shuttlev1.DeployStatus_DEPLOY_STATUS_SUCCESS {
		t.Errorf("status = %v, want SUCCESS", res.Status)
	}
	if len(res.Logs) != len(lines) {
		t.Errorf("terminal logs = %d, want %d", len(res.Logs), len(lines))
	}
}

func TestStreamDeployResult_stderrMarkerFails(t *testing.T) {
	lines := []LogLine{
		{Stream: "stdout", Text: "pulling"},
		{Stream: "stderr", Text: "[shuttle] compose error: boom"},
	}
	sink := &recordSink{}
	if err := streamDeployResult(sink, "dep-2", "svc", feed(lines), nil); err != nil {
		t.Fatalf("streamDeployResult: %v", err)
	}
	res := sink.result()
	if res == nil || res.Status != shuttlev1.DeployStatus_DEPLOY_STATUS_FAILED {
		t.Errorf("result = %+v, want FAILED", res)
	}
}

func TestStreamDeployResult_startErrorNoLogs(t *testing.T) {
	sink := &recordSink{}
	if err := streamDeployResult(sink, "dep-3", "svc", nil, errors.New("driver boom")); err != nil {
		t.Fatalf("streamDeployResult: %v", err)
	}
	if chunks := sink.logChunks(); len(chunks) != 0 {
		t.Errorf("start error should emit no live chunks, got %d", len(chunks))
	}
	res := sink.result()
	if res == nil || res.Status != shuttlev1.DeployStatus_DEPLOY_STATUS_FAILED || res.Error == "" {
		t.Errorf("result = %+v, want FAILED with error", res)
	}
}
