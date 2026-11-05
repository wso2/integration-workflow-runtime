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
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	batchpb "go.temporal.io/api/batch/v1"
	commonpb "go.temporal.io/api/common/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// execution holds the --workflow-id and --run-id flags of the commands that act on one workflow.
type execution struct {
	workflowID, runID string
}

func (e *execution) flags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&e.workflowID, "workflow-id", "w", "", "the workflow's ID (required)")
	cmd.Flags().StringVarP(&e.runID, "run-id", "r", "", "one run of it; the latest run when not set")
	_ = cmd.MarkFlagRequired("workflow-id")
}

func (e execution) proto() *commonpb.WorkflowExecution {
	return &commonpb.WorkflowExecution{WorkflowId: e.workflowID, RunId: e.runID}
}

func (c *cli) workflowCommand() *cobra.Command {
	wf := &cobra.Command{Use: "workflow", Short: "List, inspect, signal, cancel and terminate workflows"}
	wf.AddCommand(c.workflowListCommand(), c.workflowCountCommand(), c.workflowDescribeCommand(),
		c.workflowShowCommand(), c.workflowSignalCommand(), c.workflowCancelCommand(), c.workflowTerminateCommand(),
		c.workflowResetCommand())
	return wf
}

func (c *cli) workflowListCommand() *cobra.Command {
	var query string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workflows, newest first",
		Long: "Lists the namespace's workflows, filtered by a visibility --query such as\n" +
			"  'ExecutionStatus=\"Running\" AND WorkflowKind=\"orders.Order\"'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 0 {
				return errors.New("--limit must not be negative")
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			client := workflowservice.NewWorkflowServiceClient(conn)
			var all []*workflowpb.WorkflowExecutionInfo
			var token []byte
			for limit == 0 || len(all) < limit {
				res, err := client.ListWorkflowExecutions(ctx, &workflowservice.ListWorkflowExecutionsRequest{
					Namespace: s.Namespace, Query: query, PageSize: 100, NextPageToken: token,
				})
				if err != nil {
					return fmt.Errorf("unable to list workflows: %w", err)
				}
				all = append(all, res.GetExecutions()...)
				if token = res.GetNextPageToken(); len(token) == 0 {
					break
				}
			}
			if limit > 0 && len(all) > limit {
				all = all[:limit]
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				msgs := make([]proto.Message, len(all))
				for i, w := range all {
					msgs[i] = w
				}
				return writeJSON(out, true, msgs...)
			}
			rows := [][]string{{"STATUS", "WORKFLOW ID", "TYPE", "START TIME"}}
			for _, w := range all {
				rows = append(rows, []string{enumName(w.GetStatus().String(), "WORKFLOW_EXECUTION_STATUS_"),
					w.GetExecution().GetWorkflowId(), w.GetType().GetName(), timestamp(w.GetStartTime())})
			}
			return table(out, rows)
		},
	}
	cmd.Flags().StringVarP(&query, "query", "q", "", "a visibility query")
	cmd.Flags().IntVar(&limit, "limit", 100, "the most workflows to show; 0 for all")
	return cmd
}

func (c *cli) workflowCountCommand() *cobra.Command {
	var query string
	cmd := &cobra.Command{
		Use:   "count",
		Short: "Count workflows",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			res, err := workflowservice.NewWorkflowServiceClient(conn).CountWorkflowExecutions(ctx,
				&workflowservice.CountWorkflowExecutionsRequest{Namespace: s.Namespace, Query: query})
			if err != nil {
				return fmt.Errorf("unable to count workflows: %w", err)
			}
			if c.output == outputJSON {
				return writeJSON(cmd.OutOrStdout(), false, res)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Total: %d\n", res.GetCount())
			return err
		},
	}
	cmd.Flags().StringVarP(&query, "query", "q", "", "a visibility query")
	return cmd
}

func (c *cli) workflowDescribeCommand() *cobra.Command {
	var e execution
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Show a workflow's status, type, task queue and pending work",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			res, err := workflowservice.NewWorkflowServiceClient(conn).DescribeWorkflowExecution(ctx,
				&workflowservice.DescribeWorkflowExecutionRequest{Namespace: s.Namespace, Execution: e.proto()})
			if err != nil {
				return fmt.Errorf("unable to describe workflow %s: %w", e.workflowID, err)
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, false, res)
			}
			w := res.GetWorkflowExecutionInfo()
			return table(out, [][]string{
				{"Workflow ID", w.GetExecution().GetWorkflowId()},
				{"Run ID", w.GetExecution().GetRunId()},
				{"Type", w.GetType().GetName()},
				{"Task queue", w.GetTaskQueue()},
				{"Status", enumName(w.GetStatus().String(), "WORKFLOW_EXECUTION_STATUS_")},
				{"Start time", timestamp(w.GetStartTime())},
				{"Close time", timestamp(w.GetCloseTime())},
				{"History events", strconv.FormatInt(w.GetHistoryLength(), 10)},
				{"Pending activities", strconv.Itoa(len(res.GetPendingActivities()))},
				{"Pending child workflows", strconv.Itoa(len(res.GetPendingChildren()))},
			})
		},
	}
	e.flags(cmd)
	return cmd
}

func (c *cli) workflowShowCommand() *cobra.Command {
	var e execution
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show a workflow's event history",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			client := workflowservice.NewWorkflowServiceClient(conn)
			history := &historypb.History{}
			var token []byte
			for {
				res, err := client.GetWorkflowExecutionHistory(ctx, &workflowservice.GetWorkflowExecutionHistoryRequest{
					Namespace: s.Namespace, Execution: e.proto(), NextPageToken: token,
				})
				if err != nil {
					return fmt.Errorf("unable to read the history of workflow %s: %w", e.workflowID, err)
				}
				history.Events = append(history.Events, res.GetHistory().GetEvents()...)
				if token = res.GetNextPageToken(); len(token) == 0 {
					break
				}
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, false, history)
			}
			rows := [][]string{{"ID", "TIME", "TYPE"}}
			for _, ev := range history.GetEvents() {
				rows = append(rows, []string{strconv.FormatInt(ev.GetEventId(), 10), timestamp(ev.GetEventTime()),
					enumName(ev.GetEventType().String(), "EVENT_TYPE_")})
			}
			return table(out, rows)
		},
	}
	e.flags(cmd)
	return cmd
}

func (c *cli) workflowSignalCommand() *cobra.Command {
	var t target
	var name, reason string
	var inputs []string
	cmd := &cobra.Command{
		Use:   "signal",
		Short: "Send a signal, such as an integration's data event, to a workflow or to every workflow a query matches",
		Long: "Sends --name with each --input as a JSON argument, for example\n" +
			"  workflow-cli workflow signal -w order-1 --name approval --input '{\"approved\":true}'",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := t.check(); err != nil {
				return err
			}
			payloads, err := jsonPayloads(inputs)
			if err != nil {
				return err
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if t.query != "" {
				if reason == "" {
					reason = "signal " + name + " from workflow-cli"
				}
				return c.startBatch(cmd, ctx, conn, s, t, "signal", reason, func(r *workflowservice.StartBatchOperationRequest) {
					r.Operation = &workflowservice.StartBatchOperationRequest_SignalOperation{
						SignalOperation: &batchpb.BatchOperationSignal{Signal: name, Input: payloads, Identity: identity()},
					}
				})
			}
			_, err = workflowservice.NewWorkflowServiceClient(conn).SignalWorkflowExecution(ctx,
				&workflowservice.SignalWorkflowExecutionRequest{
					Namespace: s.Namespace, WorkflowExecution: t.proto(), SignalName: name, Input: payloads,
					Identity: identity(), RequestId: requestID(),
				})
			if err != nil {
				return fmt.Errorf("unable to signal workflow %s: %w", t.workflowID, err)
			}
			return c.confirm(cmd, "Signal %s sent to workflow %s.\n", name, t.workflowID)
		},
	}
	t.flags(cmd)
	cmd.Flags().StringVar(&name, "name", "", "the signal's name (required)")
	cmd.Flags().StringArrayVarP(&inputs, "input", "i", nil, "a JSON argument; repeat for more")
	cmd.Flags().StringVar(&reason, "reason", "", "why, for a --query batch job")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func (c *cli) workflowCancelCommand() *cobra.Command {
	var t target
	var reason string
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Ask a workflow, or every workflow a query matches, to cancel; each can clean up before it closes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := t.check(); err != nil {
				return err
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if t.query != "" {
				if reason == "" {
					return errors.New("a --query batch job needs a --reason")
				}
				return c.startBatch(cmd, ctx, conn, s, t, "cancel", reason, func(r *workflowservice.StartBatchOperationRequest) {
					r.Operation = &workflowservice.StartBatchOperationRequest_CancellationOperation{
						CancellationOperation: &batchpb.BatchOperationCancellation{Identity: identity()},
					}
				})
			}
			_, err = workflowservice.NewWorkflowServiceClient(conn).RequestCancelWorkflowExecution(ctx,
				&workflowservice.RequestCancelWorkflowExecutionRequest{
					Namespace: s.Namespace, WorkflowExecution: t.proto(), Reason: reason,
					Identity: identity(), RequestId: requestID(),
				})
			if err != nil {
				return fmt.Errorf("unable to cancel workflow %s: %w", t.workflowID, err)
			}
			return c.confirm(cmd, "Cancellation requested for workflow %s.\n", t.workflowID)
		},
	}
	t.flags(cmd)
	cmd.Flags().StringVar(&reason, "reason", "", "why, recorded in its history (required with --query)")
	return cmd
}

func (c *cli) workflowTerminateCommand() *cobra.Command {
	var t target
	var reason string
	cmd := &cobra.Command{
		Use:   "terminate",
		Short: "End a workflow, or every workflow a query matches, at once, without letting it clean up",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := t.check(); err != nil {
				return err
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if t.query != "" {
				if reason == "" {
					return errors.New("a --query batch job needs a --reason")
				}
				return c.startBatch(cmd, ctx, conn, s, t, "terminate", reason, func(r *workflowservice.StartBatchOperationRequest) {
					r.Operation = &workflowservice.StartBatchOperationRequest_TerminationOperation{
						TerminationOperation: &batchpb.BatchOperationTermination{Identity: identity()},
					}
				})
			}
			_, err = workflowservice.NewWorkflowServiceClient(conn).TerminateWorkflowExecution(ctx,
				&workflowservice.TerminateWorkflowExecutionRequest{
					Namespace: s.Namespace, WorkflowExecution: t.proto(), Reason: reason, Identity: identity(),
				})
			if err != nil {
				return fmt.Errorf("unable to terminate workflow %s: %w", t.workflowID, err)
			}
			return c.confirm(cmd, "Workflow %s terminated.\n", t.workflowID)
		},
	}
	t.flags(cmd)
	cmd.Flags().StringVar(&reason, "reason", "", "why, recorded in its history (required with --query)")
	return cmd
}

// jsonPayloads encodes each input as a json/plain payload, the encoding ballerina/workflow and the Temporal SDKs read.
func jsonPayloads(inputs []string) (*commonpb.Payloads, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	payloads := &commonpb.Payloads{}
	for _, in := range inputs {
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(in)); err != nil {
			return nil, fmt.Errorf("--input %q is not JSON; quote a string as '\"text\"'", in)
		}
		payloads.Payloads = append(payloads.Payloads, &commonpb.Payload{
			Metadata: map[string][]byte{"encoding": []byte("json/plain")}, Data: compact.Bytes(),
		})
	}
	return payloads, nil
}

// identity names the caller in a workflow's history, as the Temporal CLI does.
func identity() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return "workflow-cli@" + host
}

// requestID makes a repeated call with the same ID a no-op on the engine; each command run is a new request.
func requestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func timestamp(t *timestamppb.Timestamp) string {
	if t == nil || t.AsTime().IsZero() {
		return ""
	}
	return t.AsTime().UTC().Format(time.RFC3339)
}

// enumName shows WORKFLOW_EXECUTION_STATUS_RUNNING as Running and EVENT_TYPE_TIMER_STARTED as TimerStarted; the
// Temporal API's enums already print in that form, which is kept as it is.
func enumName(s, prefix string) string {
	if !strings.Contains(s, "_") {
		return s
	}
	var b strings.Builder
	for _, word := range strings.Split(strings.TrimPrefix(s, prefix), "_") {
		if word == "" {
			continue
		}
		b.WriteString(word[:1] + strings.ToLower(word[1:]))
	}
	return b.String()
}
