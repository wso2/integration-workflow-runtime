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
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	enumspb "go.temporal.io/api/enums/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
)

func (c *cli) taskQueueCommand() *cobra.Command {
	tq := &cobra.Command{Use: "task-queue", Short: "See which workers poll a task queue"}
	tq.AddCommand(c.taskQueueDescribeCommand())
	return tq
}

func (c *cli) taskQueueDescribeCommand() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "List the workers polling a task queue for workflow and activity tasks",
		Long: "Lists the workers that polled --task-queue in the last few minutes. An integration's workers poll the\n" +
			"task queue named in its Config.toml; none here means no integration serves those workflows.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			client := workflowservice.NewWorkflowServiceClient(conn)
			kinds := []struct {
				name string
				typ  enumspb.TaskQueueType
			}{{"workflow", enumspb.TASK_QUEUE_TYPE_WORKFLOW}, {"activity", enumspb.TASK_QUEUE_TYPE_ACTIVITY}}
			var responses []proto.Message
			rows := [][]string{{"TYPE", "WORKER", "LAST POLL", "RATE PER SECOND"}}
			for _, k := range kinds {
				res, err := client.DescribeTaskQueue(ctx, &workflowservice.DescribeTaskQueueRequest{
					Namespace: s.Namespace, TaskQueue: &taskqueuepb.TaskQueue{Name: name}, TaskQueueType: k.typ,
				})
				if err != nil {
					return fmt.Errorf("unable to describe task queue %s: %w", name, err)
				}
				responses = append(responses, res)
				for _, p := range res.GetPollers() {
					rows = append(rows, []string{k.name, p.GetIdentity(), timestamp(p.GetLastAccessTime()),
						strconv.FormatFloat(p.GetRatePerSecond(), 'f', -1, 64)})
				}
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, true, responses...)
			}
			if len(rows) == 1 {
				_, err := fmt.Fprintf(out, "No worker has polled task queue %s recently.\n", name)
				return err
			}
			return table(out, rows)
		},
	}
	cmd.Flags().StringVarP(&name, "task-queue", "t", "", "the task queue (required)")
	_ = cmd.MarkFlagRequired("task-queue")
	return cmd
}
