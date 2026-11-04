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
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/server/api/adminservice/v1"
	commonspb "go.temporal.io/server/api/common/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// historyDLQ is the queue type of the history task dead-letter queues (Temporal's persistence.QueueTypeHistoryDLQ).
const historyDLQ = 2

// taskCategories are the --dlq-type values: Temporal's history task categories and their IDs.
var taskCategories = map[string]int32{
	"transfer": 1, "timer": 2, "replication": 3, "visibility": 4, "archival": 5, "outbound": 7,
}

func categoryName(id int32) string {
	for name, v := range taskCategories {
		if v == id {
			return name
		}
	}
	return strconv.Itoa(int(id))
}

func (c *cli) adminCommand() *cobra.Command {
	admin := &cobra.Command{
		Use:   "admin",
		Short: "Repair the engine: dead-letter queues and history shards (needs an admin token for every namespace)",
		Long: "Commands for incidents, from Temporal's tdbg: the history tasks the engine could not process, and the\n" +
			"shards that hold workflow state. They call the admin API, which needs the admin role in every namespace.",
	}
	admin.AddCommand(c.dlqCommand(), c.shardCommand())
	return admin
}

func (c *cli) shardCommand() *cobra.Command {
	shard := &cobra.Command{Use: "shard", Short: "Find and reload history shards"}
	shard.AddCommand(c.shardDescribeCommand(), c.shardCloseCommand())
	return shard
}

func shardID(cmd *cobra.Command, id *int32) {
	cmd.Flags().Int32Var(id, "shard-id", 0, "the shard, from 1 to the cluster's history shards (required)")
	_ = cmd.MarkFlagRequired("shard-id")
}

func (c *cli) shardDescribeCommand() *cobra.Command {
	var id int32
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Show which history node owns a shard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			admin := adminservice.NewAdminServiceClient(conn)
			res, err := admin.GetShard(ctx, &adminservice.GetShardRequest{ShardId: id})
			if err != nil {
				return fmt.Errorf("unable to read shard %d: %w", id, err)
			}
			host, err := admin.DescribeHistoryHost(ctx, &adminservice.DescribeHistoryHostRequest{ShardId: id})
			if err != nil {
				return fmt.Errorf("unable to find the node of shard %d: %w", id, err)
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, true, res, host)
			}
			info := res.GetShardInfo()
			return table(out, [][]string{
				{"Shard", strconv.Itoa(int(info.GetShardId()))},
				{"Node", host.GetAddress()},
				{"Node's shards", strconv.Itoa(int(host.GetShardsNumber()))},
				{"Owner", info.GetOwner()},
				{"Range ID", strconv.FormatInt(info.GetRangeId(), 10)},
				{"Updated", timestamp(info.GetUpdateTime())},
			})
		},
	}
	shardID(cmd, &id)
	return cmd
}

func (c *cli) shardCloseCommand() *cobra.Command {
	var id int32
	cmd := &cobra.Command{
		Use:   "close",
		Short: "Close a shard so its history node loads it again from the database",
		Long: "Closes the shard on its node; the node reloads it at the next call that needs it. Use it when a\n" +
			"shard's workflows stopped making progress after a database failover.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if _, err := adminservice.NewAdminServiceClient(conn).CloseShard(ctx,
				&adminservice.CloseShardRequest{ShardId: id}); err != nil {
				return fmt.Errorf("unable to close shard %d: %w", id, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Shard %d closed; its node reloads it on the next call.\n", id)
			return err
		},
	}
	shardID(cmd, &id)
	return cmd
}

// dlqKey holds the flags that name one dead-letter queue.
type dlqKey struct {
	category, source, target string
}

func (k *dlqKey) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&k.category, "dlq-type", "", "the task category: "+choices(taskCategories)+" (required)")
	cmd.Flags().StringVar(&k.source, "source-cluster", "", "the source cluster (default: this cluster)")
	cmd.Flags().StringVar(&k.target, "target-cluster", "", "the target cluster (default: this cluster)")
	_ = cmd.MarkFlagRequired("dlq-type")
}

// resolve checks the category and fills the clusters from the engine's own cluster name.
func (k dlqKey) resolve(ctx context.Context, conn *grpc.ClientConn) (*commonspb.HistoryDLQKey, error) {
	id, ok := taskCategories[k.category]
	if !ok {
		return nil, fmt.Errorf("--dlq-type %q must be one of %s", k.category, choices(taskCategories))
	}
	if k.source == "" || k.target == "" {
		info, err := workflowservice.NewWorkflowServiceClient(conn).GetClusterInfo(ctx,
			&workflowservice.GetClusterInfoRequest{})
		if err != nil {
			return nil, fmt.Errorf("unable to read the cluster's name: %w", err)
		}
		if k.source == "" {
			k.source = info.GetClusterName()
		}
		if k.target == "" {
			k.target = info.GetClusterName()
		}
	}
	return &commonspb.HistoryDLQKey{TaskCategory: id, SourceCluster: k.source, TargetCluster: k.target}, nil
}

func (c *cli) dlqCommand() *cobra.Command {
	dlq := &cobra.Command{Use: "dlq", Short: "Read, retry and purge the history tasks the engine could not process"}
	dlq.AddCommand(c.dlqListCommand(), c.dlqReadCommand(), c.dlqMergeCommand(), c.dlqPurgeCommand(), c.dlqJobCommand())
	return dlq
}

func (c *cli) dlqListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the dead-letter queues and how many tasks each holds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			queues, err := listDLQs(ctx, adminservice.NewAdminServiceClient(conn))
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				msgs := make([]proto.Message, len(queues))
				for i, q := range queues {
					msgs[i] = q
				}
				return writeJSON(out, true, msgs...)
			}
			if len(queues) == 0 {
				_, err := fmt.Fprintln(out, "No dead-letter queue exists; no history task has failed for good.")
				return err
			}
			rows := [][]string{{"DLQ TYPE", "SOURCE", "TARGET", "TASKS", "LAST MESSAGE ID"}}
			for _, q := range queues {
				f := strings.SplitN(q.GetQueueName(), "_", 4)
				category, source, target := q.GetQueueName(), "", ""
				if len(f) == 4 {
					if id, err := strconv.Atoi(f[0]); err == nil {
						category = categoryName(int32(id)) //nolint:gosec // a task category ID
					}
					source, target = f[1], f[2]
				}
				rows = append(rows, []string{category, source, target, strconv.FormatInt(q.GetMessageCount(), 10),
					strconv.FormatInt(q.GetLastMessageId(), 10)})
			}
			return table(out, rows)
		},
	}
}

func listDLQs(ctx context.Context, admin adminservice.AdminServiceClient) ([]*adminservice.ListQueuesResponse_QueueInfo,
	error) {
	var all []*adminservice.ListQueuesResponse_QueueInfo
	var token []byte
	for {
		res, err := admin.ListQueues(ctx, &adminservice.ListQueuesRequest{QueueType: historyDLQ, PageSize: 100,
			NextPageToken: token})
		if err != nil {
			return nil, fmt.Errorf("unable to list the dead-letter queues: %w", err)
		}
		all = append(all, res.GetQueues()...)
		if token = res.GetNextPageToken(); len(token) == 0 {
			return all, nil
		}
	}
}

func (c *cli) dlqReadCommand() *cobra.Command {
	var k dlqKey
	var limit int32
	cmd := &cobra.Command{
		Use:   "read",
		Short: "List the tasks in a dead-letter queue",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit <= 0 {
				return errors.New("--limit must be positive")
			}
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			key, err := k.resolve(ctx, conn)
			if err != nil {
				return err
			}
			res, err := adminservice.NewAdminServiceClient(conn).GetDLQTasks(ctx,
				&adminservice.GetDLQTasksRequest{DlqKey: key, PageSize: limit})
			if err != nil {
				return fmt.Errorf("unable to read the %s dead-letter queue: %w", k.category, err)
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, false, res)
			}
			if len(res.GetDlqTasks()) == 0 {
				_, err := fmt.Fprintf(out, "The %s dead-letter queue is empty.\n", k.category)
				return err
			}
			rows := [][]string{{"MESSAGE ID", "SHARD", "TASK BYTES"}}
			for _, t := range res.GetDlqTasks() {
				rows = append(rows, []string{strconv.FormatInt(t.GetMetadata().GetMessageId(), 10),
					strconv.Itoa(int(t.GetPayload().GetShardId())), strconv.Itoa(len(t.GetPayload().GetBlob().GetData()))})
			}
			if err := table(out, rows); err != nil {
				return err
			}
			if len(res.GetNextPageToken()) > 0 {
				_, err = fmt.Fprintf(out, "\nMore tasks follow; raise --limit (%d) to see them.\n", limit)
			}
			return err
		},
	}
	k.flags(cmd)
	cmd.Flags().Int32Var(&limit, "limit", 100, "the most tasks to show")
	return cmd
}

// dlqJob starts a merge or purge job up to --last-message-id, by default the queue's last task, after --yes.
func (c *cli) dlqJob(verb string, start func(context.Context, adminservice.AdminServiceClient,
	*commonspb.HistoryDLQKey, *commonspb.HistoryDLQTaskMetadata) ([]byte, error)) *cobra.Command {
	var k dlqKey
	var last int64
	var yes bool
	cmd := &cobra.Command{
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return fmt.Errorf("add --yes to %s the tasks; `admin dlq read` shows them first", verb)
			}
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			key, err := k.resolve(ctx, conn)
			if err != nil {
				return err
			}
			admin := adminservice.NewAdminServiceClient(conn)
			if last == 0 {
				if last, err = lastMessageID(ctx, admin, key); err != nil {
					return err
				}
			}
			token, err := start(ctx, admin, key, &commonspb.HistoryDLQTaskMetadata{MessageId: last})
			if err != nil {
				return fmt.Errorf("unable to %s the %s dead-letter queue: %w", verb, k.category, err)
			}
			job := base64.StdEncoding.EncodeToString(token)
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Started to %s the %s dead-letter queue up to message %d; "+
				"follow it with\n  workflow-cli admin dlq job describe --job-token %s\n", verb, k.category, last, job)
			return err
		},
	}
	k.flags(cmd)
	cmd.Flags().Int64Var(&last, "last-message-id", 0, "the last task to include (default: the queue's last)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "confirm")
	return cmd
}

func lastMessageID(ctx context.Context, admin adminservice.AdminServiceClient, key *commonspb.HistoryDLQKey) (int64,
	error) {
	queues, err := listDLQs(ctx, admin)
	if err != nil {
		return 0, err
	}
	prefix := fmt.Sprintf("%d_%s_%s_", key.GetTaskCategory(), key.GetSourceCluster(), key.GetTargetCluster())
	for _, q := range queues {
		if strings.HasPrefix(q.GetQueueName(), prefix) && q.GetMessageCount() > 0 {
			return q.GetLastMessageId(), nil
		}
	}
	return 0, fmt.Errorf("the %s dead-letter queue is empty", categoryName(key.GetTaskCategory()))
}

func (c *cli) dlqMergeCommand() *cobra.Command {
	var batch int32
	cmd := c.dlqJob("merge", func(ctx context.Context, admin adminservice.AdminServiceClient,
		key *commonspb.HistoryDLQKey, last *commonspb.HistoryDLQTaskMetadata) ([]byte, error) {
		res, err := admin.MergeDLQTasks(ctx, &adminservice.MergeDLQTasksRequest{DlqKey: key,
			InclusiveMaxTaskMetadata: last, BatchSize: batch})
		return res.GetJobToken(), err
	})
	cmd.Use = "merge"
	cmd.Short = "Run the queue's tasks again, after the cause is fixed; tasks that fail again return to it"
	cmd.Flags().Int32Var(&batch, "batch-size", 0, "tasks per batch (the engine's default when 0)")
	return cmd
}

func (c *cli) dlqPurgeCommand() *cobra.Command {
	cmd := c.dlqJob("purge", func(ctx context.Context, admin adminservice.AdminServiceClient,
		key *commonspb.HistoryDLQKey, last *commonspb.HistoryDLQTaskMetadata) ([]byte, error) {
		res, err := admin.PurgeDLQTasks(ctx, &adminservice.PurgeDLQTasksRequest{DlqKey: key,
			InclusiveMaxTaskMetadata: last})
		return res.GetJobToken(), err
	})
	cmd.Use = "purge"
	cmd.Short = "Delete the queue's tasks without running them"
	return cmd
}

func (c *cli) dlqJobCommand() *cobra.Command {
	job := &cobra.Command{Use: "job", Short: "Follow and cancel merge and purge jobs"}
	var describeToken, cancelToken, reason string
	describe := &cobra.Command{
		Use:   "describe",
		Short: "Show a merge or purge job's progress",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := base64.StdEncoding.DecodeString(describeToken)
			if err != nil {
				return errors.New("--job-token is not the token a merge or purge printed")
			}
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			res, err := adminservice.NewAdminServiceClient(conn).DescribeDLQJob(ctx,
				&adminservice.DescribeDLQJobRequest{JobToken: token})
			if err != nil {
				return fmt.Errorf("unable to describe the job: %w", err)
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, false, res)
			}
			return table(out, [][]string{
				{"Operation", enumName(res.GetOperationType().String(), "DLQ_OPERATION_TYPE_")},
				{"State", enumName(res.GetOperationState().String(), "DLQ_OPERATION_STATE_")},
				{"DLQ type", categoryName(res.GetDlqKey().GetTaskCategory())},
				{"Processed", strconv.FormatInt(res.GetMessagesProcessed(), 10)},
				{"Last processed", strconv.FormatInt(res.GetLastProcessedMessageId(), 10)},
				{"Up to", strconv.FormatInt(res.GetMaxMessageId(), 10)},
				{"Start time", timestamp(res.GetStartTime())},
				{"End time", timestamp(res.GetEndTime())},
			})
		},
	}
	describe.Flags().StringVar(&describeToken, "job-token", "", "the token merge or purge printed (required)")
	_ = describe.MarkFlagRequired("job-token")
	cancelCmd := &cobra.Command{
		Use:   "cancel",
		Short: "Stop a merge or purge job",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := base64.StdEncoding.DecodeString(cancelToken)
			if err != nil {
				return errors.New("--job-token is not the token a merge or purge printed")
			}
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if _, err := adminservice.NewAdminServiceClient(conn).CancelDLQJob(ctx,
				&adminservice.CancelDLQJobRequest{JobToken: token, Reason: reason}); err != nil {
				return fmt.Errorf("unable to cancel the job: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Job cancelled.")
			return err
		},
	}
	cancelCmd.Flags().StringVar(&cancelToken, "job-token", "", "the token merge or purge printed (required)")
	cancelCmd.Flags().StringVar(&reason, "reason", "", "why (required)")
	_ = cancelCmd.MarkFlagRequired("job-token")
	_ = cancelCmd.MarkFlagRequired("reason")
	job.AddCommand(describe, cancelCmd)
	return job
}
