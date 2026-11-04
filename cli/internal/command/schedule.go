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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	schedulepb "go.temporal.io/api/schedule/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// overlapPolicies are the --overlap-policy values, as the Temporal CLI spells them.
var overlapPolicies = map[string]enumspb.ScheduleOverlapPolicy{
	"Skip":           enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	"BufferOne":      enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ONE,
	"BufferAll":      enumspb.SCHEDULE_OVERLAP_POLICY_BUFFER_ALL,
	"CancelOther":    enumspb.SCHEDULE_OVERLAP_POLICY_CANCEL_OTHER,
	"TerminateOther": enumspb.SCHEDULE_OVERLAP_POLICY_TERMINATE_OTHER,
	"AllowAll":       enumspb.SCHEDULE_OVERLAP_POLICY_ALLOW_ALL,
}

func (c *cli) scheduleCommand() *cobra.Command {
	sc := &cobra.Command{Use: "schedule", Short: "Start workflows on a calendar or an interval"}
	sc.AddCommand(c.scheduleCreateCommand(), c.scheduleListCommand(), c.scheduleDescribeCommand(),
		c.scheduleToggleCommand(), c.scheduleTriggerCommand(), c.scheduleDeleteCommand())
	return sc
}

// scheduleID is the --schedule-id flag every schedule command but list takes.
func scheduleID(cmd *cobra.Command, id *string) {
	cmd.Flags().StringVarP(id, "schedule-id", "s", "", "the schedule's ID (required)")
	_ = cmd.MarkFlagRequired("schedule-id")
}

func (c *cli) scheduleCreateCommand() *cobra.Command {
	var id, workflowID, workflowType, taskQueue, overlap, notes string
	var crons, inputs []string
	var intervals []time.Duration
	var paused bool
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a schedule that starts a workflow",
		Long: "Starts --type on --task-queue at each --cron time (\"0 9 * * MON-FRI\") or every --interval (\"1h\"), for\n" +
			"example\n" +
			"  workflow-cli schedule create -s nightly-report --cron '0 2 * * *' --workflow-id nightly-report \\\n" +
			"    --type reports.Nightly --task-queue reports\n" +
			"Each run's workflow ID is --workflow-id with the scheduled time appended.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(crons) == 0 && len(intervals) == 0 {
				return errors.New("give at least one --cron or --interval")
			}
			for _, d := range intervals {
				if d <= 0 {
					return errors.New("--interval must be positive")
				}
			}
			policy, ok := overlapPolicies[overlap]
			if overlap != "" && !ok {
				return fmt.Errorf("--overlap-policy %q must be one of %s", overlap, choices(overlapPolicies))
			}
			payloads, err := jsonPayloads(inputs)
			if err != nil {
				return err
			}
			spec := &schedulepb.ScheduleSpec{CronString: crons}
			for _, d := range intervals {
				spec.Interval = append(spec.Interval, &schedulepb.IntervalSpec{Interval: durationpb.New(d)})
			}
			schedule := &schedulepb.Schedule{
				Spec: spec,
				Action: &schedulepb.ScheduleAction{Action: &schedulepb.ScheduleAction_StartWorkflow{
					StartWorkflow: &workflowpb.NewWorkflowExecutionInfo{
						WorkflowId: workflowID, WorkflowType: &commonpb.WorkflowType{Name: workflowType},
						TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}, Input: payloads,
					},
				}},
				Policies: &schedulepb.SchedulePolicies{OverlapPolicy: policy},
				State:    &schedulepb.ScheduleState{Paused: paused, Notes: notes},
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if _, err := workflowservice.NewWorkflowServiceClient(conn).CreateSchedule(ctx,
				&workflowservice.CreateScheduleRequest{Namespace: s.Namespace, ScheduleId: id, Schedule: schedule,
					Identity: identity(), RequestId: requestID()}); err != nil {
				return fmt.Errorf("unable to create schedule %s: %w", id, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Schedule %s created.\n", id)
			return err
		},
	}
	scheduleID(cmd, &id)
	f := cmd.Flags()
	f.StringArrayVar(&crons, "cron", nil, "a cron expression; repeat for more")
	f.DurationSliceVar(&intervals, "interval", nil, "an interval such as 30m; repeat for more")
	f.StringVar(&workflowID, "workflow-id", "", "the started workflows' ID prefix (required)")
	f.StringVar(&workflowType, "type", "", "the workflow type to start (required)")
	f.StringVarP(&taskQueue, "task-queue", "t", "", "the task queue its integration polls (required)")
	f.StringArrayVarP(&inputs, "input", "i", nil, "a JSON argument for each run; repeat for more")
	f.StringVar(&overlap, "overlap-policy", "", "when a run is still open at the next time: "+choices(overlapPolicies)+
		" (Skip when not set)")
	f.BoolVar(&paused, "paused", false, "create it paused")
	f.StringVar(&notes, "notes", "", "a note shown with the schedule")
	for _, required := range []string{"workflow-id", "type", "task-queue"} {
		_ = cmd.MarkFlagRequired(required)
	}
	return cmd
}

func (c *cli) scheduleListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the schedules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			client := workflowservice.NewWorkflowServiceClient(conn)
			var all []*schedulepb.ScheduleListEntry
			var token []byte
			for {
				res, err := client.ListSchedules(ctx, &workflowservice.ListSchedulesRequest{
					Namespace: s.Namespace, MaximumPageSize: 100, NextPageToken: token,
				})
				if err != nil {
					return fmt.Errorf("unable to list schedules: %w", err)
				}
				all = append(all, res.GetSchedules()...)
				if token = res.GetNextPageToken(); len(token) == 0 {
					break
				}
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				msgs := make([]proto.Message, len(all))
				for i, e := range all {
					msgs[i] = e
				}
				return writeJSON(out, true, msgs...)
			}
			rows := [][]string{{"SCHEDULE ID", "WORKFLOW TYPE", "PAUSED", "NEXT RUN"}}
			for _, e := range all {
				next := ""
				if times := e.GetInfo().GetFutureActionTimes(); len(times) > 0 {
					next = timestamp(times[0])
				}
				rows = append(rows, []string{e.GetScheduleId(), e.GetInfo().GetWorkflowType().GetName(),
					strconv.FormatBool(e.GetInfo().GetPaused()), next})
			}
			return table(out, rows)
		},
	}
}

func (c *cli) scheduleDescribeCommand() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Show a schedule: its times, action, state and recent runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			res, err := workflowservice.NewWorkflowServiceClient(conn).DescribeSchedule(ctx,
				&workflowservice.DescribeScheduleRequest{Namespace: s.Namespace, ScheduleId: id})
			if err != nil {
				return fmt.Errorf("unable to describe schedule %s: %w", id, err)
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, false, res)
			}
			sc, info := res.GetSchedule(), res.GetInfo()
			start := sc.GetAction().GetStartWorkflow()
			var every []string
			for _, iv := range sc.GetSpec().GetInterval() {
				every = append(every, iv.GetInterval().AsDuration().String())
			}
			next := ""
			if times := info.GetFutureActionTimes(); len(times) > 0 {
				next = timestamp(times[0])
			}
			rows := [][]string{
				{"Schedule ID", id},
				{"Cron", strings.Join(sc.GetSpec().GetCronString(), "; ")},
				{"Interval", strings.Join(every, ", ")},
				{"Workflow type", start.GetWorkflowType().GetName()},
				{"Task queue", start.GetTaskQueue().GetName()},
				{"Workflow ID", start.GetWorkflowId()},
				{"Paused", strconv.FormatBool(sc.GetState().GetPaused())},
				{"Notes", sc.GetState().GetNotes()},
				{"Runs", strconv.FormatInt(info.GetActionCount(), 10)},
				{"Next run", next},
			}
			if err := table(out, rows); err != nil {
				return err
			}
			if len(info.GetRecentActions()) == 0 {
				return nil
			}
			recent := [][]string{{"SCHEDULED", "STARTED", "WORKFLOW ID", "STATUS"}}
			for _, a := range info.GetRecentActions() {
				recent = append(recent, []string{timestamp(a.GetScheduleTime()), timestamp(a.GetActualTime()),
					a.GetStartWorkflowResult().GetWorkflowId(),
					enumName(a.GetStartWorkflowStatus().String(), "WORKFLOW_EXECUTION_STATUS_")})
			}
			_, _ = fmt.Fprintln(out)
			return table(out, recent)
		},
	}
	scheduleID(cmd, &id)
	return cmd
}

func (c *cli) scheduleToggleCommand() *cobra.Command {
	var id, reason string
	var pause, unpause bool
	cmd := &cobra.Command{
		Use:   "toggle",
		Short: "Pause or unpause a schedule",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if pause == unpause {
				return errors.New("give --pause or --unpause")
			}
			if reason == "" {
				reason = "toggled with workflow-cli"
			}
			patch := &schedulepb.SchedulePatch{Unpause: reason}
			verb := "unpaused"
			if pause {
				patch, verb = &schedulepb.SchedulePatch{Pause: reason}, "paused"
			}
			return c.patchSchedule(cmd, id, patch, verb)
		},
	}
	scheduleID(cmd, &id)
	cmd.Flags().BoolVar(&pause, "pause", false, "stop starting workflows")
	cmd.Flags().BoolVar(&unpause, "unpause", false, "start workflows again")
	cmd.Flags().StringVar(&reason, "reason", "", "why, kept as the schedule's notes")
	return cmd
}

func (c *cli) scheduleTriggerCommand() *cobra.Command {
	var id, overlap string
	cmd := &cobra.Command{
		Use:   "trigger",
		Short: "Start the schedule's workflow now, once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			policy, ok := overlapPolicies[overlap]
			if overlap != "" && !ok {
				return fmt.Errorf("--overlap-policy %q must be one of %s", overlap, choices(overlapPolicies))
			}
			return c.patchSchedule(cmd, id, &schedulepb.SchedulePatch{
				TriggerImmediately: &schedulepb.TriggerImmediatelyRequest{OverlapPolicy: policy},
			}, "triggered")
		},
	}
	scheduleID(cmd, &id)
	cmd.Flags().StringVar(&overlap, "overlap-policy", "", "overrides the schedule's policy for this run")
	return cmd
}

func (c *cli) patchSchedule(cmd *cobra.Command, id string, patch *schedulepb.SchedulePatch, verb string) error {
	conn, s, ctx, cancel, err := c.connect(cmd)
	if err != nil {
		return err
	}
	defer cancel()
	defer func() { _ = conn.Close() }()
	if _, err := workflowservice.NewWorkflowServiceClient(conn).PatchSchedule(ctx, &workflowservice.PatchScheduleRequest{
		Namespace: s.Namespace, ScheduleId: id, Patch: patch, Identity: identity(), RequestId: requestID(),
	}); err != nil {
		return fmt.Errorf("unable to update schedule %s: %w", id, err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Schedule %s %s.\n", id, verb)
	return err
}

func (c *cli) scheduleDeleteCommand() *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a schedule; workflows it started keep running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			if _, err := workflowservice.NewWorkflowServiceClient(conn).DeleteSchedule(ctx,
				&workflowservice.DeleteScheduleRequest{Namespace: s.Namespace, ScheduleId: id,
					Identity: identity()}); err != nil {
				return fmt.Errorf("unable to delete schedule %s: %w", id, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Schedule %s deleted.\n", id)
			return err
		},
	}
	scheduleID(cmd, &id)
	return cmd
}
