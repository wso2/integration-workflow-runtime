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
	"slices"
	"strings"
	"testing"

	batchpb "go.temporal.io/api/batch/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// opsState records the reset, batch and schedule calls the fake engine receives.
type opsState struct {
	resets    []*workflowservice.ResetWorkflowExecutionRequest
	batches   []*workflowservice.StartBatchOperationRequest
	stops     []*workflowservice.StopBatchOperationRequest
	schedules map[string]*schedulepb.Schedule
	crons     []string // the cron strings create requests sent, before the engine's canonical form
	patches   []*workflowservice.PatchScheduleRequest
}

func (f workflowFake) ResetWorkflowExecution(ctx context.Context, r *workflowservice.ResetWorkflowExecutionRequest) (
	*workflowservice.ResetWorkflowExecutionResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops.resets = append(f.ops.resets, r)
	return &workflowservice.ResetWorkflowExecutionResponse{RunId: "run-reset"}, nil
}

func (f workflowFake) StartBatchOperation(ctx context.Context, r *workflowservice.StartBatchOperationRequest) (
	*workflowservice.StartBatchOperationResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops.batches = append(f.ops.batches, r)
	return &workflowservice.StartBatchOperationResponse{}, nil
}

func (f workflowFake) ListBatchOperations(ctx context.Context, _ *workflowservice.ListBatchOperationsRequest) (
	*workflowservice.ListBatchOperationsResponse, error) {
	f.record(ctx)
	return &workflowservice.ListBatchOperationsResponse{OperationInfo: []*batchpb.BatchOperationInfo{{JobId: "job-1",
		State: enumspb.BATCH_OPERATION_STATE_RUNNING, OperationType: enumspb.BATCH_OPERATION_TYPE_TERMINATE_WORKFLOW,
		StartTime: timestamppb.New(started)}}}, nil
}

func (f workflowFake) DescribeBatchOperation(ctx context.Context, r *workflowservice.DescribeBatchOperationRequest) (
	*workflowservice.DescribeBatchOperationResponse, error) {
	f.record(ctx)
	if r.GetJobId() != "job-1" {
		return nil, serviceerror.NewNotFound("batch job not found")
	}
	return &workflowservice.DescribeBatchOperationResponse{JobId: "job-1", Query: `WorkflowKind="orders.Order"`,
		OperationType: enumspb.BATCH_OPERATION_TYPE_TERMINATE_WORKFLOW, State: enumspb.BATCH_OPERATION_STATE_COMPLETED,
		TotalOperationCount: 3, CompleteOperationCount: 2, FailureOperationCount: 1, Reason: "retired"}, nil
}

func (f workflowFake) StopBatchOperation(ctx context.Context, r *workflowservice.StopBatchOperationRequest) (
	*workflowservice.StopBatchOperationResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops.stops = append(f.ops.stops, r)
	return &workflowservice.StopBatchOperationResponse{}, nil
}

func (f workflowFake) CreateSchedule(ctx context.Context, r *workflowservice.CreateScheduleRequest) (
	*workflowservice.CreateScheduleResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.ops.schedules[r.GetScheduleId()]; ok {
		return nil, serviceerror.NewAlreadyExists("schedule already exists")
	}
	f.ops.crons = append(f.ops.crons, r.GetSchedule().GetSpec().GetCronString()...)
	f.ops.schedules[r.GetScheduleId()] = canonical(r.GetSchedule())
	return &workflowservice.CreateScheduleResponse{}, nil
}

// canonical stores a schedule as Temporal does: each cron string becomes a structured calendar and is cleared. The
// fake knows only the crons the tests use, written as Temporal's canonicalizeSpec (v1.32.1) writes them.
func canonical(sc *schedulepb.Schedule) *schedulepb.Schedule {
	sc = proto.Clone(sc).(*schedulepb.Schedule)
	spec := sc.GetSpec()
	for _, cron := range spec.GetCronString() {
		switch cron {
		case "0 2 * * *":
			spec.StructuredCalendar = append(spec.StructuredCalendar, &schedulepb.StructuredCalendarSpec{
				Second: []*schedulepb.Range{{Start: 0}}, Minute: []*schedulepb.Range{{Start: 0}},
				Hour: []*schedulepb.Range{{Start: 2}}, DayOfMonth: []*schedulepb.Range{{Start: 1, End: 31}},
				Month: []*schedulepb.Range{{Start: 1, End: 12}}, DayOfWeek: []*schedulepb.Range{{Start: 0, End: 6}},
			})
		default:
			panic("the fake has no canonical form for cron " + cron)
		}
	}
	spec.CronString = nil
	return sc
}

func (f workflowFake) ListSchedules(ctx context.Context, _ *workflowservice.ListSchedulesRequest) (
	*workflowservice.ListSchedulesResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	res := &workflowservice.ListSchedulesResponse{}
	for id, sc := range f.ops.schedules {
		res.Schedules = append(res.Schedules, &schedulepb.ScheduleListEntry{ScheduleId: id,
			Info: &schedulepb.ScheduleListInfo{WorkflowType: sc.GetAction().GetStartWorkflow().GetWorkflowType(),
				Paused: sc.GetState().GetPaused(), FutureActionTimes: []*timestamppb.Timestamp{timestamppb.New(started)}}})
	}
	return res, nil
}

func (f workflowFake) DescribeSchedule(ctx context.Context, r *workflowservice.DescribeScheduleRequest) (
	*workflowservice.DescribeScheduleResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	sc, ok := f.ops.schedules[r.GetScheduleId()]
	if !ok {
		return nil, serviceerror.NewNotFound("schedule not found")
	}
	return &workflowservice.DescribeScheduleResponse{Schedule: sc, Info: &schedulepb.ScheduleInfo{ActionCount: 1,
		RecentActions: []*schedulepb.ScheduleActionResult{{ScheduleTime: timestamppb.New(started),
			ActualTime: timestamppb.New(started), StartWorkflowStatus: enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED}}}}, nil
}

func (f workflowFake) PatchSchedule(ctx context.Context, r *workflowservice.PatchScheduleRequest) (
	*workflowservice.PatchScheduleResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ops.patches = append(f.ops.patches, r)
	return &workflowservice.PatchScheduleResponse{}, nil
}

func (f workflowFake) DeleteSchedule(ctx context.Context, r *workflowservice.DeleteScheduleRequest) (
	*workflowservice.DeleteScheduleResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.ops.schedules[r.GetScheduleId()]; !ok {
		return nil, serviceerror.NewNotFound("schedule not found")
	}
	delete(f.ops.schedules, r.GetScheduleId())
	return &workflowservice.DeleteScheduleResponse{}, nil
}

func TestWorkflowReset(t *testing.T) {
	tc := newTestCLI(t)
	tc.engine.workflows.events = []*historypb.HistoryEvent{
		{EventId: 1, EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED},
		{EventId: 4, EventType: enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED},
		{EventId: 9, EventType: enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED},
	}
	for args, want := range map[string]int64{
		"--type FirstWorkflowTask": 4, "--type LastWorkflowTask": 9, "--event-id 7": 7,
	} {
		out := tc.must(append([]string{"workflow", "reset", "-w", "order-1", "--reason", "fixed"}, strings.Fields(args)...)...)
		r := tc.engine.ops.resets[len(tc.engine.ops.resets)-1]
		if r.GetWorkflowTaskFinishEventId() != want || r.GetReason() != "fixed" || r.GetRequestId() == "" ||
			!strings.Contains(out, "the new run is run-reset") {
			t.Errorf("%s: reset to %d (%s), want %d", args, r.GetWorkflowTaskFinishEventId(), out, want)
		}
	}
	tc.engine.workflows.events = tc.engine.workflows.events[:1]
	for args, want := range map[string]string{
		"workflow reset -w order-1 --reason x --type FirstWorkflowTask": "no completed workflow task",
		"workflow reset -w order-1 --reason x":                          "give --type or --event-id",
		"workflow reset -w order-1 --reason x --type Mid":               `must be FirstWorkflowTask or LastWorkflowTask`,
		"workflow reset -w order-1 --type FirstWorkflowTask":            `required flag(s) "reason" not set`,
		"workflow reset -w missing --reason x --type LastWorkflowTask":  "unable to read the history of workflow missing",
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
}

func TestBatchJobs(t *testing.T) {
	tc := newTestCLI(t)
	query := `WorkflowKind="orders.Order"`
	if code, _, errOut := tc.run("workflow", "terminate", "-q", query, "--reason", "retired"); code != 1 ||
		!strings.Contains(errOut, "3 workflows match; add --yes to terminate them all") {
		t.Fatalf("without --yes = %d %q", code, errOut)
	}
	if len(tc.engine.ops.batches) != 0 {
		t.Fatal("a batch started without --yes")
	}
	out := tc.must("workflow", "terminate", "-q", query, "--reason", "retired", "--yes")
	b := tc.engine.ops.batches[0]
	if b.GetVisibilityQuery() != query || b.GetReason() != "retired" || b.GetTerminationOperation() == nil ||
		!strings.Contains(out, "started to terminate 3 workflows") || !strings.Contains(out, b.GetJobId()) {
		t.Fatalf("terminate batch = %v\n%s", b, out)
	}
	tc.must("workflow", "cancel", "-q", query, "--reason", "withdrawn", "-y")
	if b := tc.engine.ops.batches[1]; b.GetCancellationOperation() == nil || b.GetReason() != "withdrawn" {
		t.Fatalf("cancel batch = %v", b)
	}
	tc.must("workflow", "signal", "-q", query, "--name", "wake", "-i", `{"now":true}`, "-y")
	if b := tc.engine.ops.batches[2]; b.GetSignalOperation().GetSignal() != "wake" ||
		string(b.GetSignalOperation().GetInput().GetPayloads()[0].GetData()) != `{"now":true}` ||
		b.GetReason() != "signal wake from workflow-cli" {
		t.Fatalf("signal batch = %v", b)
	}

	if out := tc.must("batch", "list"); !strings.Contains(out, "job-1   TerminateWorkflow  Running") {
		t.Fatalf("batch list:\n%s", out)
	}
	out = tc.must("batch", "describe", "--job-id", "job-1")
	for _, want := range []string{"State       Completed", "Workflows   3", "Done        2", "Failed      1"} {
		if !strings.Contains(out, want) {
			t.Errorf("batch describe lacks %q:\n%s", want, out)
		}
	}
	tc.must("batch", "terminate", "--job-id", "job-1", "--reason", "enough")
	if s := tc.engine.ops.stops[0]; s.GetJobId() != "job-1" || s.GetReason() != "enough" {
		t.Fatalf("stop = %v", s)
	}
	for args, want := range map[string]string{
		"workflow terminate":                 "give --workflow-id or --query",
		"workflow terminate -w order-1 -q x": "give --workflow-id or --query",
		"workflow terminate -q x -r run-1":   "--run-id applies to --workflow-id only",
		"workflow cancel -q x -y":            "needs a --reason",
		"workflow terminate -q x -y":         "needs a --reason",
		"batch describe --job-id job-2":      "unable to describe batch job job-2",
		"batch terminate --job-id job-1":     `required flag(s) "reason" not set`,
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
}

func TestCronFields(t *testing.T) {
	r := func(start, end, step int32) *schedulepb.Range {
		return &schedulepb.Range{Start: start, End: end, Step: step}
	}
	for _, tt := range []struct {
		cal  *schedulepb.StructuredCalendarSpec
		want string
	}{
		{&schedulepb.StructuredCalendarSpec{}, "0 0 * * *"},
		{&schedulepb.StructuredCalendarSpec{Minute: []*schedulepb.Range{r(0, 59, 15)}, Hour: []*schedulepb.Range{r(9, 17, 0)},
			DayOfWeek: []*schedulepb.Range{r(1, 5, 0)}, Comment: "office hours"}, "*/15 9-17 * * 1-5 # office hours"},
		{&schedulepb.StructuredCalendarSpec{Second: []*schedulepb.Range{r(30, 0, 0)}, Minute: []*schedulepb.Range{r(0, 0, 0),
			r(30, 0, 0)}, DayOfMonth: []*schedulepb.Range{r(1, 0, 0)}, Month: []*schedulepb.Range{r(1, 12, 3)}}, "30 0,30 0 1 */3 * *"},
		{&schedulepb.StructuredCalendarSpec{Year: []*schedulepb.Range{r(2027, 0, 0)}}, "0 0 * * * 2027"},
	} {
		if got := calendars(&schedulepb.ScheduleSpec{StructuredCalendar: []*schedulepb.StructuredCalendarSpec{tt.cal}}); len(got) != 1 ||
			got[0] != tt.want {
			t.Errorf("calendar %v = %q, want %q", tt.cal, got, tt.want)
		}
	}
}

func TestSchedules(t *testing.T) {
	tc := newTestCLI(t)
	tc.must("schedule", "create", "-s", "nightly", "--cron", "0 2 * * *", "--interval", "12h", "--workflow-id",
		"nightly-report", "--type", "reports.Nightly", "-t", "reports", "-i", `{"days":1}`,
		"--overlap-policy", "BufferOne", "--notes", "for finance")
	sc := tc.engine.ops.schedules["nightly"]
	start := sc.GetAction().GetStartWorkflow()
	if !slices.Equal(tc.engine.ops.crons, []string{"0 2 * * *"}) || sc.GetSpec().GetInterval()[0].GetInterval().AsDuration().Hours() != 12 ||
		start.GetWorkflowType().GetName() != "reports.Nightly" || start.GetTaskQueue().GetName() != "reports" ||
		string(start.GetInput().GetPayloads()[0].GetData()) != `{"days":1}` ||
		sc.GetPolicies().GetOverlapPolicy() != enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ONE || sc.GetState().GetNotes() != "for finance" {
		t.Fatalf("created schedule = %v", sc)
	}
	if out := tc.must("schedule", "list"); !strings.Contains(out, "nightly      reports.Nightly  false   2026-11-04T09:30:00Z") {
		t.Fatalf("schedule list:\n%s", out)
	}
	out := tc.must("schedule", "describe", "-s", "nightly")
	for _, want := range []string{"Calendar       0 2 * * *", "Interval       12h0m0s", "Time zone      UTC", "Workflow type  reports.Nightly",
		"Runs           1", "Completed"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %q:\n%s", want, out)
		}
	}
	tc.must("schedule", "toggle", "-s", "nightly", "--pause", "--reason", "month end")
	tc.must("schedule", "toggle", "-s", "nightly", "--unpause")
	tc.must("schedule", "trigger", "-s", "nightly", "--overlap-policy", "AllowAll")
	p := tc.engine.ops.patches
	if p[0].GetPatch().GetPause() != "month end" || p[1].GetPatch().GetUnpause() == "" ||
		p[2].GetPatch().GetTriggerImmediately().GetOverlapPolicy() != enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL {
		t.Fatalf("patches = %v", p)
	}
	tc.must("schedule", "delete", "-s", "nightly")
	if _, ok := tc.engine.ops.schedules["nightly"]; ok {
		t.Fatal("the schedule was not deleted")
	}
	base := "schedule create -s s2 --workflow-id w --type T -t q"
	for args, want := range map[string]string{
		base:                    "give at least one --cron or --interval",
		base + " --interval 0s": "--interval must be positive",
		base + " --cron x --overlap-policy Maybe":   `--overlap-policy "Maybe" must be one of AllowAll`,
		base + " --cron x -i {":                     "is not JSON",
		"schedule create -s s3 --cron x":            `required flag(s)`,
		"schedule toggle -s s2":                     "give --pause or --unpause",
		"schedule trigger -s s2 --overlap-policy x": "must be one of",
		"schedule describe -s gone":                 "unable to describe schedule gone",
		"schedule delete -s gone":                   "unable to delete schedule gone",
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
}
