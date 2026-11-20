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
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
	batchpb "go.temporal.io/api/batch/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/wso2/integration-workflow-runtime/cli/internal/clientconfig"
)

// target is one workflow (--workflow-id, --run-id) or every workflow a --query matches, as a batch job.
type target struct {
	execution
	query string
	yes   bool
}

func (t *target) flags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&t.workflowID, "workflow-id", "w", "", "the workflow's ID")
	cmd.Flags().StringVarP(&t.runID, "run-id", "r", "", "one run of it; the latest run when not set")
	cmd.Flags().StringVarP(&t.query, "query", "q", "", "every workflow this visibility query matches, as a batch job")
	cmd.Flags().BoolVarP(&t.yes, "yes", "y", false, "confirm a --query batch without being asked")
}

func (t target) check() error {
	switch {
	case (t.workflowID == "") == (t.query == ""):
		return errors.New("give --workflow-id or --query, not both")
	case t.query != "" && t.runID != "":
		return errors.New("--run-id applies to --workflow-id only")
	}
	return nil
}

// startBatch counts the workflows the query matches and, with --yes, starts a batch job for them on the engine.
func (c *cli) startBatch(cmd *cobra.Command, ctx context.Context, conn *grpc.ClientConn, s clientconfig.Settings,
	t target, verb, reason string, apply func(*workflowservice.StartBatchOperationRequest)) error {
	client := workflowservice.NewWorkflowServiceClient(conn)
	count, err := client.CountWorkflowExecutions(ctx, &workflowservice.CountWorkflowExecutionsRequest{
		Namespace: s.Namespace, Query: t.query,
	})
	if err != nil {
		return fmt.Errorf("unable to count the workflows to %s: %w", verb, err)
	}
	if !t.yes {
		return fmt.Errorf("%d workflows match; add --yes to %s them all", count.GetCount(), verb)
	}
	req := &workflowservice.StartBatchOperationRequest{
		Namespace: s.Namespace, VisibilityQuery: t.query, JobId: requestID(), Reason: reason,
	}
	apply(req)
	if _, err := client.StartBatchOperation(ctx, req); err != nil {
		return fmt.Errorf("unable to start the batch job: %w", err)
	}
	if c.output == outputJSON {
		b, err := json.MarshalIndent(map[string]any{"jobId": req.GetJobId(), "count": count.GetCount()}, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Batch job %s started to %s %d workflows; follow it with\n"+
		"  workflow-cli batch describe --job-id %s\n", req.GetJobId(), verb, count.GetCount(), req.GetJobId())
	return err
}

func (c *cli) batchCommand() *cobra.Command {
	b := &cobra.Command{Use: "batch", Short: "Follow and stop batch jobs started with --query"}
	b.AddCommand(c.batchListCommand(), c.batchDescribeCommand(), c.batchTerminateCommand())
	return b
}

func (c *cli) batchListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the namespace's batch jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			client := workflowservice.NewWorkflowServiceClient(conn)
			var all []*batchpb.BatchOperationInfo
			var token []byte
			for {
				res, err := client.ListBatchOperations(ctx, &workflowservice.ListBatchOperationsRequest{
					Namespace: s.Namespace, PageSize: 100, NextPageToken: token,
				})
				if err != nil {
					return fmt.Errorf("unable to list batch jobs: %w", err)
				}
				all = append(all, res.GetOperationInfo()...)
				if token = res.GetNextPageToken(); len(token) == 0 {
					break
				}
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				msgs := make([]proto.Message, len(all))
				for i, b := range all {
					msgs[i] = b
				}
				return writeJSON(out, true, msgs...)
			}
			rows := [][]string{{"JOB ID", "OPERATION", "STATE", "START TIME", "CLOSE TIME"}}
			for _, b := range all {
				rows = append(rows, []string{b.GetJobId(), enumName(b.GetOperationType().String(), "BATCH_OPERATION_TYPE_"),
					enumName(b.GetState().String(), "BATCH_OPERATION_STATE_"), timestamp(b.GetStartTime()),
					timestamp(b.GetCloseTime())})
			}
			return table(out, rows)
		},
	}
}

func (c *cli) batchDescribeCommand() *cobra.Command {
	var jobID string
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Show a batch job's progress",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			res, err := workflowservice.NewWorkflowServiceClient(conn).DescribeBatchOperation(ctx,
				&workflowservice.DescribeBatchOperationRequest{Namespace: s.Namespace, JobId: jobID})
			if err != nil {
				return fmt.Errorf("unable to describe batch job %s: %w", jobID, err)
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, false, res)
			}
			return table(out, [][]string{
				{"Job ID", res.GetJobId()},
				{"Operation", enumName(res.GetOperationType().String(), "BATCH_OPERATION_TYPE_")},
				{"State", enumName(res.GetState().String(), "BATCH_OPERATION_STATE_")},
				{"Query", res.GetQuery()},
				{"Reason", res.GetReason()},
				{"Workflows", strconv.FormatInt(res.GetTotalOperationCount(), 10)},
				{"Done", strconv.FormatInt(res.GetCompleteOperationCount(), 10)},
				{"Failed", strconv.FormatInt(res.GetFailureOperationCount(), 10)},
				{"Start time", timestamp(res.GetStartTime())},
				{"Close time", timestamp(res.GetCloseTime())},
			})
		},
	}
	cmd.Flags().StringVar(&jobID, "job-id", "", "the batch job (required)")
	_ = cmd.MarkFlagRequired("job-id")
	return cmd
}

func (c *cli) batchTerminateCommand() *cobra.Command {
	var jobID, reason string
	cmd := &cobra.Command{
		Use:   "terminate",
		Short: "Stop a batch job; workflows it already changed stay changed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if _, err := workflowservice.NewWorkflowServiceClient(conn).StopBatchOperation(ctx,
				&workflowservice.StopBatchOperationRequest{Namespace: s.Namespace, JobId: jobID, Reason: reason,
					Identity: identity()}); err != nil {
				return fmt.Errorf("unable to stop batch job %s: %w", jobID, err)
			}
			return c.confirm(cmd, "Batch job %s stopped.\n", jobID)
		},
	}
	cmd.Flags().StringVar(&jobID, "job-id", "", "the batch job (required)")
	cmd.Flags().StringVar(&reason, "reason", "", "why (required)")
	_ = cmd.MarkFlagRequired("job-id")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}
