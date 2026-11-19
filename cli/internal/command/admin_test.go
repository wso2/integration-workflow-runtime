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
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/server/api/adminservice/v1"
	commonspb "go.temporal.io/server/api/common/v1"
	enumsspb "go.temporal.io/server/api/enums/v1"
	persistencespb "go.temporal.io/server/api/persistence/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// adminState records the fake engine's admin calls.
type adminState struct {
	closed  []int32
	reads   []*adminservice.GetDLQTasksRequest
	purges  []*adminservice.PurgeDLQTasksRequest
	merges  []*adminservice.MergeDLQTasksRequest
	cancels []*adminservice.CancelDLQJobRequest
	pages   []string // the page tokens ListQueues received
	empty   bool
}

func (f adminFake) GetShard(ctx context.Context, r *adminservice.GetShardRequest) (*adminservice.GetShardResponse, error) {
	f.record(ctx)
	return &adminservice.GetShardResponse{ShardInfo: &persistencespb.ShardInfo{ShardId: r.GetShardId(),
		Owner: "10.0.0.21:7234", RangeId: 7, UpdateTime: timestamppb.New(started)}}, nil
}

func (f adminFake) DescribeHistoryHost(ctx context.Context, _ *adminservice.DescribeHistoryHostRequest) (
	*adminservice.DescribeHistoryHostResponse, error) {
	f.record(ctx)
	return &adminservice.DescribeHistoryHostResponse{Address: "10.0.0.21:7234", ShardsNumber: 256}, nil
}

func (f adminFake) CloseShard(ctx context.Context, r *adminservice.CloseShardRequest) (*adminservice.CloseShardResponse,
	error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.closed = append(f.admin.closed, r.GetShardId())
	return &adminservice.CloseShardResponse{}, nil
}

// ListQueues returns one queue per page, so the transfer queue that merge and purge look up is on the second page.
func (f adminFake) ListQueues(ctx context.Context, r *adminservice.ListQueuesRequest) (*adminservice.ListQueuesResponse,
	error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.pages = append(f.admin.pages, string(r.GetNextPageToken()))
	if f.admin.empty {
		return &adminservice.ListQueuesResponse{}, nil
	}
	if len(r.GetNextPageToken()) == 0 {
		return &adminservice.ListQueuesResponse{NextPageToken: []byte("page-2"),
			Queues: []*adminservice.ListQueuesResponse_QueueInfo{
				{QueueName: "2_active_active_def", MessageCount: 0, LastMessageId: 9}}}, nil
	}
	return &adminservice.ListQueuesResponse{Queues: []*adminservice.ListQueuesResponse_QueueInfo{
		{QueueName: "1_active_active_abc", MessageCount: 3, LastMessageId: 42}}}, nil
}

func (f adminFake) GetDLQTasks(ctx context.Context, r *adminservice.GetDLQTasksRequest) (*adminservice.GetDLQTasksResponse,
	error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.reads = append(f.admin.reads, r)
	return &adminservice.GetDLQTasksResponse{NextPageToken: []byte{1}, DlqTasks: []*commonspb.HistoryDLQTask{
		{Metadata: &commonspb.HistoryDLQTaskMetadata{MessageId: 40},
			Payload: &commonspb.HistoryTask{ShardId: 12, Blob: &commonpb.DataBlob{Data: []byte("task")}}},
	}}, nil
}

func (f adminFake) PurgeDLQTasks(ctx context.Context, r *adminservice.PurgeDLQTasksRequest) (
	*adminservice.PurgeDLQTasksResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.purges = append(f.admin.purges, r)
	return &adminservice.PurgeDLQTasksResponse{JobToken: []byte("purge-job")}, nil
}

func (f adminFake) MergeDLQTasks(ctx context.Context, r *adminservice.MergeDLQTasksRequest) (
	*adminservice.MergeDLQTasksResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.merges = append(f.admin.merges, r)
	return &adminservice.MergeDLQTasksResponse{JobToken: []byte("merge-job")}, nil
}

func (f adminFake) DescribeDLQJob(ctx context.Context, r *adminservice.DescribeDLQJobRequest) (
	*adminservice.DescribeDLQJobResponse, error) {
	f.record(ctx)
	return &adminservice.DescribeDLQJobResponse{OperationType: enumsspb.DLQ_OPERATION_TYPE_MERGE,
		OperationState: enumsspb.DLQ_OPERATION_STATE_COMPLETED, DlqKey: &commonspb.HistoryDLQKey{TaskCategory: 1},
		MessagesProcessed: 3, LastProcessedMessageId: 42, MaxMessageId: 42}, nil
}

func (f adminFake) CancelDLQJob(ctx context.Context, r *adminservice.CancelDLQJobRequest) (
	*adminservice.CancelDLQJobResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.admin.cancels = append(f.admin.cancels, r)
	return &adminservice.CancelDLQJobResponse{}, nil
}

func TestShards(t *testing.T) {
	tc := newTestCLI(t)
	out := tc.must("admin", "shard", "describe", "--shard-id", "12")
	for _, want := range []string{"Shard          12", "Node           10.0.0.21:7234", "Node's shards  256", "Range ID       7"} {
		if !strings.Contains(out, want) {
			t.Errorf("shard describe lacks %q:\n%s", want, out)
		}
	}
	var shard []map[string]any
	if err := json.Unmarshal([]byte(tc.must("admin", "shard", "describe", "--shard-id", "12", "-o", "json")),
		&shard); err != nil || len(shard) != 2 || shard[1]["address"] != "10.0.0.21:7234" {
		t.Fatalf("json shard describe = %v, %v", shard, err)
	}
	if info, _ := shard[0]["shardInfo"].(map[string]any); info["shardId"] != float64(12) {
		t.Fatalf("json shard info = %v", shard[0])
	}
	tc.must("admin", "shard", "close", "--shard-id", "12")
	if c := tc.engine.admin.closed; len(c) != 1 || c[0] != 12 {
		t.Fatalf("closed = %v", c)
	}
}

func TestDeadLetterQueues(t *testing.T) {
	tc := newTestCLI(t)
	out := tc.must("admin", "dlq", "list")
	for _, want := range []string{"transfer  active  active  3      42", "timer     active  active  0      9"} {
		if !strings.Contains(out, want) {
			t.Errorf("dlq list lacks %q:\n%s", want, out)
		}
	}
	if p := tc.engine.admin.pages; !slices.Equal(p, []string{"", "page-2"}) {
		t.Fatalf("dlq list read pages %q, want both", p)
	}
	var queues []map[string]any
	if err := json.Unmarshal([]byte(tc.must("admin", "dlq", "list", "-o", "json")), &queues); err != nil ||
		len(queues) != 2 || queues[1]["queueName"] != "1_active_active_abc" {
		t.Fatalf("json dlq list = %v, %v", queues, err)
	}
	var tasks map[string]any
	if err := json.Unmarshal([]byte(tc.must("admin", "dlq", "read", "--dlq-type", "transfer", "-o", "json")),
		&tasks); err != nil || len(tasks["dlqTasks"].([]any)) != 1 {
		t.Fatalf("json dlq read = %v, %v", tasks, err)
	}
	tc.engine.admin.reads = nil
	out = tc.must("admin", "dlq", "read", "--dlq-type", "transfer", "--limit", "5")
	if r := tc.engine.admin.reads[0]; r.GetPageSize() != 5 || r.GetDlqKey().GetTaskCategory() != 1 ||
		r.GetDlqKey().GetSourceCluster() != "active" || r.GetDlqKey().GetTargetCluster() != "active" {
		t.Fatalf("read request = %v", r)
	}
	if !strings.Contains(out, "40          12     4") || !strings.Contains(out, "raise --limit (5)") {
		t.Fatalf("dlq read:\n%s", out)
	}

	if code, _, errOut := tc.run("admin", "dlq", "merge", "--dlq-type", "transfer"); code != 1 ||
		!strings.Contains(errOut, "add --yes to merge") {
		t.Fatalf("merge without --yes = %d %q", code, errOut)
	}
	out = tc.must("admin", "dlq", "merge", "--dlq-type", "transfer", "--yes", "--batch-size", "10")
	m := tc.engine.admin.merges[0]
	if m.GetInclusiveMaxTaskMetadata().GetMessageId() != 42 || m.GetBatchSize() != 10 ||
		!strings.Contains(out, base64.StdEncoding.EncodeToString([]byte("merge-job"))) {
		t.Fatalf("merge = %v\n%s", m, out)
	}
	tc.must("admin", "dlq", "purge", "--dlq-type", "transfer", "--last-message-id", "41", "-y",
		"--source-cluster", "east", "--target-cluster", "west")
	p := tc.engine.admin.purges[0]
	if p.GetInclusiveMaxTaskMetadata().GetMessageId() != 41 || p.GetDlqKey().GetSourceCluster() != "east" ||
		p.GetDlqKey().GetTargetCluster() != "west" {
		t.Fatalf("purge = %v", p)
	}
	// Message 0 is the first a queue holds: an explicit 0 bounds the job to it.
	tc.must("admin", "dlq", "purge", "--dlq-type", "transfer", "--last-message-id", "0", "-y")
	tc.must("admin", "dlq", "merge", "--dlq-type", "transfer", "--last-message-id", "0", "-y")
	if p, m := tc.engine.admin.purges[1], tc.engine.admin.merges[1]; p.GetInclusiveMaxTaskMetadata().GetMessageId() != 0 ||
		m.GetInclusiveMaxTaskMetadata().GetMessageId() != 0 {
		t.Fatalf("an explicit message 0 reached the engine as %v and %v", p, m)
	}
	var started map[string]any
	if err := json.Unmarshal([]byte(tc.must("admin", "dlq", "purge", "--dlq-type", "transfer", "-y", "-o", "json")),
		&started); err != nil || started["lastMessageId"] != float64(42) || started["jobToken"] == "" {
		t.Fatalf("json purge = %v, %v", started, err)
	}
	token := base64.StdEncoding.EncodeToString([]byte("merge-job"))
	out = tc.must("admin", "dlq", "job", "describe", "--job-token", token)
	for _, want := range []string{"Operation       Merge", "State           Completed", "DLQ type        transfer",
		"Processed       3"} {
		if !strings.Contains(out, want) {
			t.Errorf("job describe lacks %q:\n%s", want, out)
		}
	}
	var job map[string]any
	if err := json.Unmarshal([]byte(tc.must("admin", "dlq", "job", "describe", "--job-token", token, "-o", "json")),
		&job); err != nil || job["operationType"] != "DLQ_OPERATION_TYPE_MERGE" || job["messagesProcessed"] != "3" {
		t.Fatalf("json job describe = %v, %v", job, err)
	}
	tc.must("admin", "dlq", "job", "cancel", "--job-token", token, "--reason", "wrong queue")
	if c := tc.engine.admin.cancels[0]; string(c.GetJobToken()) != "merge-job" || c.GetReason() != "wrong queue" {
		t.Fatalf("cancel = %v", c)
	}
	for args, want := range map[string]string{
		"admin dlq read --dlq-type disk":                              `--dlq-type "disk" must be one of archival`,
		"admin dlq read --dlq-type timer --limit 0":                   "--limit must be positive",
		"admin dlq purge --dlq-type timer -y":                         "the timer dead-letter queue is empty",
		"admin dlq purge --dlq-type transfer -y --last-message-id -1": "must not be negative",
		"admin dlq job describe --job-token !!":                       "is not the token",
		"admin dlq job cancel --job-token !! --reason x":              "is not the token",
		"admin shard describe":                                        `required flag(s) "shard-id" not set`,
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
	tc.engine.admin.empty = true
	if out := tc.must("admin", "dlq", "list"); !strings.Contains(out, "No dead-letter queue exists") {
		t.Fatalf("empty dlq list:\n%s", out)
	}
}
