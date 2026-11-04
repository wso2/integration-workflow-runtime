// Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// workflowState is the fake engine's workflows: their info, history, and the calls made on them.
type workflowState struct {
	infos   []*workflowpb.WorkflowExecutionInfo
	events  []*historypb.HistoryEvent
	signals []*workflowservice.SignalWorkflowExecutionRequest
	cancels []*workflowservice.RequestCancelWorkflowExecutionRequest
	ends    []*workflowservice.TerminateWorkflowExecutionRequest
	queries []string
	pollers map[enumspb.TaskQueueType][]*taskqueuepb.PollerInfo
}

var started = time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)

func newWorkflowState() *workflowState {
	w := &workflowState{pollers: map[enumspb.TaskQueueType][]*taskqueuepb.PollerInfo{}}
	for i, st := range []enumspb.WorkflowExecutionStatus{enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
		enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED} {
		w.infos = append(w.infos, &workflowpb.WorkflowExecutionInfo{
			Execution: &commonpb.WorkflowExecution{WorkflowId: fmt.Sprintf("order-%d", i+1), RunId: fmt.Sprintf("run-%d", i+1)},
			Type:      &commonpb.WorkflowType{Name: "orders.Order"}, Status: st, TaskQueue: "orders",
			StartTime: timestamppb.New(started.Add(time.Duration(i) * time.Minute)), HistoryLength: 5,
		})
	}
	for i, t := range []enumspb.EventType{enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
		enumspb.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED} {
		w.events = append(w.events, &historypb.HistoryEvent{EventId: int64(i + 1), EventType: t,
			EventTime: timestamppb.New(started.Add(time.Duration(i) * time.Second))})
	}
	return w
}

func (w *workflowState) find(id string) *workflowpb.WorkflowExecutionInfo {
	for _, info := range w.infos {
		if info.GetExecution().GetWorkflowId() == id {
			return info
		}
	}
	return nil
}

// ListWorkflowExecutions returns two workflows per page and records each query.
func (f workflowFake) ListWorkflowExecutions(ctx context.Context, r *workflowservice.ListWorkflowExecutionsRequest) (
	*workflowservice.ListWorkflowExecutionsResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	w := f.workflows
	w.queries = append(w.queries, r.GetQuery())
	start := 0
	if len(r.GetNextPageToken()) > 0 {
		start = int(r.GetNextPageToken()[0])
	}
	end := min(start+2, len(w.infos))
	res := &workflowservice.ListWorkflowExecutionsResponse{Executions: w.infos[start:end]}
	if end < len(w.infos) {
		res.NextPageToken = []byte{byte(end)}
	}
	return res, nil
}

func (f workflowFake) CountWorkflowExecutions(ctx context.Context, r *workflowservice.CountWorkflowExecutionsRequest) (
	*workflowservice.CountWorkflowExecutionsResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workflows.queries = append(f.workflows.queries, r.GetQuery())
	return &workflowservice.CountWorkflowExecutionsResponse{Count: int64(len(f.workflows.infos))}, nil
}

func (f workflowFake) DescribeWorkflowExecution(ctx context.Context, r *workflowservice.DescribeWorkflowExecutionRequest) (
	*workflowservice.DescribeWorkflowExecutionResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	info := f.workflows.find(r.GetExecution().GetWorkflowId())
	if info == nil {
		return nil, serviceerror.NewNotFound("workflow not found")
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: info,
		PendingActivities: []*workflowpb.PendingActivityInfo{{ActivityId: "1"}}}, nil
}

// GetWorkflowExecutionHistory returns one event per page.
func (f workflowFake) GetWorkflowExecutionHistory(ctx context.Context,
	r *workflowservice.GetWorkflowExecutionHistoryRequest) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workflows.find(r.GetExecution().GetWorkflowId()) == nil {
		return nil, serviceerror.NewNotFound("workflow not found")
	}
	i := 0
	if len(r.GetNextPageToken()) > 0 {
		i = int(r.GetNextPageToken()[0])
	}
	res := &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{Events: f.workflows.events[i : i+1]},
	}
	if i+1 < len(f.workflows.events) {
		res.NextPageToken = []byte{byte(i + 1)}
	}
	return res, nil
}

func (f workflowFake) SignalWorkflowExecution(ctx context.Context, r *workflowservice.SignalWorkflowExecutionRequest) (
	*workflowservice.SignalWorkflowExecutionResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workflows.find(r.GetWorkflowExecution().GetWorkflowId()) == nil {
		return nil, serviceerror.NewNotFound("workflow not found")
	}
	f.workflows.signals = append(f.workflows.signals, r)
	return &workflowservice.SignalWorkflowExecutionResponse{}, nil
}

func (f workflowFake) RequestCancelWorkflowExecution(ctx context.Context,
	r *workflowservice.RequestCancelWorkflowExecutionRequest) (*workflowservice.RequestCancelWorkflowExecutionResponse,
	error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workflows.cancels = append(f.workflows.cancels, r)
	return &workflowservice.RequestCancelWorkflowExecutionResponse{}, nil
}

func (f workflowFake) TerminateWorkflowExecution(ctx context.Context, r *workflowservice.TerminateWorkflowExecutionRequest) (
	*workflowservice.TerminateWorkflowExecutionResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if info := f.workflows.find(r.GetWorkflowExecution().GetWorkflowId()); info == nil ||
		info.GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return nil, serviceerror.NewNotFound("workflow execution already completed")
	}
	f.workflows.ends = append(f.workflows.ends, r)
	return &workflowservice.TerminateWorkflowExecutionResponse{}, nil
}

func (f workflowFake) DescribeTaskQueue(ctx context.Context, r *workflowservice.DescribeTaskQueueRequest) (
	*workflowservice.DescribeTaskQueueResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	return &workflowservice.DescribeTaskQueueResponse{Pollers: f.workflows.pollers[r.GetTaskQueueType()]}, nil
}

func TestWorkflowListAndCount(t *testing.T) {
	tc := newTestCLI(t)
	out := tc.must("workflow", "list", "-q", `WorkflowKind="orders.Order"`)
	for _, want := range []string{"STATUS      WORKFLOW ID  TYPE          START TIME",
		"Running     order-1      orders.Order  2026-11-04T09:30:00Z", "Completed   order-2",
		"Terminated  order-3"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	if q := tc.engine.workflows.queries; q[0] != `WorkflowKind="orders.Order"` || len(q) != 2 {
		t.Fatalf("queries = %q, want the query on both pages", q)
	}
	if out := tc.must("workflow", "list", "--limit", "1"); strings.Contains(out, "order-2") {
		t.Fatalf("--limit 1 listed more:\n%s", out)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(tc.must("workflow", "list", "-o", "json", "--limit", "0")), &list); err != nil ||
		len(list) != 3 {
		t.Fatalf("json list = %v, %v", list, err)
	}
	if out := tc.must("workflow", "count"); out != "Total: 3\n" {
		t.Fatalf("count = %q", out)
	}
	if code, _, errOut := tc.run("workflow", "list", "--limit", "-1"); code != 1 || !strings.Contains(errOut, "negative") {
		t.Fatalf("negative limit = %d %q", code, errOut)
	}
}

func TestWorkflowDescribeAndShow(t *testing.T) {
	tc := newTestCLI(t)
	out := tc.must("workflow", "describe", "-w", "order-1")
	for _, want := range []string{"Run ID                   run-1", "Task queue               orders",
		"Status                   Running", "Pending activities       1"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %q:\n%s", want, out)
		}
	}
	out = tc.must("workflow", "show", "-w", "order-1")
	for _, want := range []string{"1   2026-11-04T09:30:00Z  WorkflowExecutionStarted",
		"3   2026-11-04T09:30:02Z  WorkflowExecutionSignaled"} {
		if !strings.Contains(out, want) {
			t.Errorf("show over three pages lacks %q:\n%s", want, out)
		}
	}
	var history map[string][]any
	if err := json.Unmarshal([]byte(tc.must("workflow", "show", "-w", "order-1", "-o", "json")), &history); err != nil ||
		len(history["events"]) != 3 {
		t.Fatalf("json history = %v, %v", history, err)
	}
	for args, want := range map[string]string{
		"workflow describe -w missing": "unable to describe workflow missing",
		"workflow show -w missing":     "unable to read the history of workflow missing",
		"workflow describe":            `required flag(s) "workflow-id" not set`,
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
}

func TestWorkflowSignalCancelTerminate(t *testing.T) {
	tc := newTestCLI(t)
	out := tc.must("workflow", "signal", "-w", "order-1", "--name", "approval", "-i", `{ "approved": true }`, "-i", `"ann"`)
	if out != "Signal approval sent to workflow order-1.\n" {
		t.Fatalf("signal = %q", out)
	}
	sig := tc.engine.workflows.signals[0]
	if sig.GetSignalName() != "approval" || len(sig.GetInput().GetPayloads()) != 2 ||
		string(sig.GetInput().GetPayloads()[0].GetData()) != `{"approved":true}` ||
		string(sig.GetInput().GetPayloads()[0].GetMetadata()["encoding"]) != "json/plain" ||
		!strings.HasPrefix(sig.GetIdentity(), "workflow-cli@") || len(sig.GetRequestId()) != 32 {
		t.Fatalf("signal request = %v", sig)
	}
	tc.must("workflow", "cancel", "-w", "order-1", "-r", "run-1", "--reason", "customer withdrew")
	if c := tc.engine.workflows.cancels[0]; c.GetReason() != "customer withdrew" ||
		c.GetWorkflowExecution().GetRunId() != "run-1" {
		t.Fatalf("cancel request = %v", c)
	}
	if out := tc.must("workflow", "terminate", "-w", "order-1", "--reason", "stuck"); out != "Workflow order-1 terminated.\n" {
		t.Fatalf("terminate = %q", out)
	}
	if e := tc.engine.workflows.ends[0]; e.GetReason() != "stuck" {
		t.Fatalf("terminate request = %v", e)
	}
	for args, want := range map[string]string{
		"workflow signal -w order-1 --name x -i {oops": "is not JSON",
		"workflow signal -w missing --name x":          "unable to signal workflow missing",
		"workflow signal -w order-1":                   `required flag(s) "name" not set`,
		"workflow terminate -w order-2":                "unable to terminate workflow order-2",
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
}

func TestTaskQueueDescribe(t *testing.T) {
	tc := newTestCLI(t)
	if out := tc.must("task-queue", "describe", "-t", "orders"); !strings.Contains(out, "No worker has polled") {
		t.Fatalf("empty task queue:\n%s", out)
	}
	tc.engine.workflows.pollers[enumspb.TASK_QUEUE_TYPE_WORKFLOW] = []*taskqueuepb.PollerInfo{
		{Identity: "orders-1@host", LastAccessTime: timestamppb.New(started), RatePerSecond: 100000}}
	tc.engine.workflows.pollers[enumspb.TASK_QUEUE_TYPE_ACTIVITY] = []*taskqueuepb.PollerInfo{
		{Identity: "orders-1@host", LastAccessTime: timestamppb.New(started), RatePerSecond: 2.5}}
	out := tc.must("task-queue", "describe", "-t", "orders")
	for _, want := range []string{"workflow  orders-1@host  2026-11-04T09:30:00Z  100000",
		"activity  orders-1@host  2026-11-04T09:30:00Z  2.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %q:\n%s", want, out)
		}
	}
	var res []map[string]any
	if err := json.Unmarshal([]byte(tc.must("task-queue", "describe", "-t", "orders", "-o", "json")), &res); err != nil ||
		len(res) != 2 {
		t.Fatalf("json = %v, %v", res, err)
	}
	if code, _, errOut := tc.run("task-queue", "describe"); code != 1 || !strings.Contains(errOut, "task-queue") {
		t.Fatalf("missing task queue = %d %q", code, errOut)
	}
}

func TestEnumName(t *testing.T) {
	for _, tt := range []struct{ in, prefix, want string }{
		{"EVENT_TYPE_TIMER_STARTED", "EVENT_TYPE_", "TimerStarted"},
		{"EVENT_TYPE_", "EVENT_TYPE_", ""},
		{"WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW", "WORKFLOW_EXECUTION_STATUS_", "ContinuedAsNew"},
		{"ContinuedAsNew", "WORKFLOW_EXECUTION_STATUS_", "ContinuedAsNew"},
		{"DlqOperationTypeMerge", "DLQ_OPERATION_TYPE_", "Merge"},
	} {
		if got := enumName(tt.in, tt.prefix); got != tt.want {
			t.Errorf("enumName(%s, %s) = %q, want %q", tt.in, tt.prefix, got, tt.want)
		}
	}
	if timestamp(nil) != "" {
		t.Error("a missing time is not empty")
	}
}
