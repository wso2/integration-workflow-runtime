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
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
)

const (
	resetFirst = "FirstWorkflowTask"
	resetLast  = "LastWorkflowTask"
)

func (c *cli) workflowResetCommand() *cobra.Command {
	var e execution
	var resetType, reason string
	var eventID int64
	cmd := &cobra.Command{
		Use:   "reset",
		Short: "Run a workflow again from an earlier point in its history, as a new run",
		Long: "Starts a new run of the workflow from the end of a workflow task, keeping the history up to it, and\n" +
			"ends the current run. Pick the point with --type FirstWorkflowTask or LastWorkflowTask, or with --event-id\n" +
			"(a WorkflowTaskCompleted, WorkflowTaskFailed or WorkflowTaskTimedOut event from `workflow show`). Signals\n" +
			"after that point are applied again to the new run.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (resetType == "") == (eventID == 0) {
				return errors.New("give --type or --event-id, not both")
			}
			if resetType != "" && resetType != resetFirst && resetType != resetLast {
				return fmt.Errorf("--type %q must be %s or %s", resetType, resetFirst, resetLast)
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			client := workflowservice.NewWorkflowServiceClient(conn)
			if resetType != "" {
				// The event belongs to one run: pin it, or a run started after the history read gets the reset.
				if e.runID == "" {
					if e.runID, err = currentRun(ctx, client, s.Namespace, e.workflowID); err != nil {
						return err
					}
				}
				if eventID, err = resetPoint(ctx, client, s.Namespace, e, resetType); err != nil {
					return err
				}
			}
			res, err := client.ResetWorkflowExecution(ctx, &workflowservice.ResetWorkflowExecutionRequest{
				Namespace: s.Namespace, WorkflowExecution: e.proto(), Reason: reason,
				WorkflowTaskFinishEventId: eventID, RequestId: requestID(), Identity: identity(),
			})
			if err != nil {
				return fmt.Errorf("unable to reset workflow %s: %w", e.workflowID, err)
			}
			if c.output == outputJSON {
				return writeJSON(cmd.OutOrStdout(), false, res)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Workflow %s reset to event %d; the new run is %s.\n",
				e.workflowID, eventID, res.GetRunId())
			return err
		},
	}
	e.flags(cmd)
	cmd.Flags().StringVar(&resetType, "type", "", "FirstWorkflowTask or LastWorkflowTask")
	cmd.Flags().Int64Var(&eventID, "event-id", 0, "the workflow task event to reset to")
	cmd.Flags().StringVar(&reason, "reason", "", "why, recorded in both runs (required)")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

// currentRun returns the run ID of the workflow's latest run.
func currentRun(ctx context.Context, client workflowservice.WorkflowServiceClient, namespace, workflowID string) (string,
	error) {
	res, err := client.DescribeWorkflowExecution(ctx, &workflowservice.DescribeWorkflowExecutionRequest{
		Namespace: namespace, Execution: &commonpb.WorkflowExecution{WorkflowId: workflowID},
	})
	if err != nil {
		return "", fmt.Errorf("unable to find the current run of workflow %s: %w", workflowID, err)
	}
	return res.GetWorkflowExecutionInfo().GetExecution().GetRunId(), nil
}

// resetPoint finds the first or last completed workflow task in the run's history.
func resetPoint(ctx context.Context, client workflowservice.WorkflowServiceClient, namespace string, e execution,
	resetType string) (int64, error) {
	var found int64
	var token []byte
	for {
		res, err := client.GetWorkflowExecutionHistory(ctx, &workflowservice.GetWorkflowExecutionHistoryRequest{
			Namespace: namespace, Execution: e.proto(), NextPageToken: token,
		})
		if err != nil {
			return 0, fmt.Errorf("unable to read the history of workflow %s: %w", e.workflowID, err)
		}
		for _, ev := range res.GetHistory().GetEvents() {
			if ev.GetEventType() != enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
				continue
			}
			found = ev.GetEventId()
			if resetType == resetFirst {
				return found, nil
			}
		}
		if token = res.GetNextPageToken(); len(token) == 0 {
			break
		}
	}
	if found == 0 {
		return 0, fmt.Errorf("workflow %s has no completed workflow task to reset to", e.workflowID)
	}
	return found, nil
}
