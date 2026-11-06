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
	"time"

	"github.com/spf13/cobra"
	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	// defaultRetention is how long closed workflows are kept, as the runtime's namespaces are usually set up.
	defaultRetention = 30 * 24 * time.Hour
	// workflowKind is the search attribute ballerina/workflow tags its workflows with.
	workflowKind = "WorkflowKind"
	// retryInterval paces the wait for a new namespace to reach the engine's namespace cache.
	retryInterval = 500 * time.Millisecond
)

func (c *cli) namespaceCommand() *cobra.Command {
	ns := &cobra.Command{Use: "namespace", Short: "Create, list, describe and update namespaces"}
	ns.AddCommand(c.namespaceCreateCommand(), c.namespaceDescribeCommand(), c.namespaceListCommand(),
		c.namespaceUpdateCommand())
	return ns
}

func (c *cli) namespaceCreateCommand() *cobra.Command {
	var retention time.Duration
	var description string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a namespace with the WorkflowKind search attribute integrations use",
		Long: "Creates --namespace with a retention period for closed workflows, and adds the WorkflowKind search\n" +
			"attribute ballerina/workflow needs. An existing namespace is left as it is; use update to change it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if retention <= 0 {
				return errors.New("--retention must be positive")
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			_, err = workflowservice.NewWorkflowServiceClient(conn).RegisterNamespace(ctx,
				&workflowservice.RegisterNamespaceRequest{
					Namespace: s.Namespace, Description: description,
					WorkflowExecutionRetentionPeriod: durationpb.New(retention),
				})
			switch {
			case status.Code(err) == codes.AlreadyExists:
				_ = c.confirm(cmd, "Namespace %s already exists; its settings are unchanged.\n", s.Namespace)
			case err != nil:
				return fmt.Errorf("unable to create namespace %s: %w", s.Namespace, err)
			default:
				_ = c.confirm(cmd, "Namespace %s created.\n", s.Namespace)
			}
			if err := addSearchAttributes(ctx, operatorservice.NewOperatorServiceClient(conn), s.Namespace,
				map[string]enumspb.IndexedValueType{workflowKind: enumspb.INDEXED_VALUE_TYPE_KEYWORD}); err != nil {
				return err
			}
			return c.confirm(cmd, "Search attribute %s is in namespace %s.\n", workflowKind, s.Namespace)
		},
	}
	cmd.Flags().DurationVar(&retention, "retention", defaultRetention, "how long closed workflows are kept")
	cmd.Flags().StringVar(&description, "description", "", "a description of the namespace")
	return cmd
}

// addSearchAttributes adds attributes to a namespace, waiting while a new namespace is not yet visible to the
// engine's services. Attributes that exist already are skipped by the engine.
func addSearchAttributes(ctx context.Context, op operatorservice.OperatorServiceClient, namespace string,
	attrs map[string]enumspb.IndexedValueType) error {
	for {
		_, err := op.AddSearchAttributes(ctx, &operatorservice.AddSearchAttributesRequest{
			Namespace: namespace, SearchAttributes: attrs,
		})
		if code := status.Code(err); err == nil || (code != codes.NotFound && code != codes.Unavailable) {
			if err != nil {
				return fmt.Errorf("unable to add search attributes to %s: %w", namespace, err)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("unable to add search attributes to %s: %w", namespace, err)
		case <-time.After(retryInterval):
		}
	}
}

func (c *cli) namespaceDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Show a namespace",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			res, err := workflowservice.NewWorkflowServiceClient(conn).DescribeNamespace(ctx,
				&workflowservice.DescribeNamespaceRequest{Namespace: s.Namespace})
			if err != nil {
				return fmt.Errorf("unable to describe namespace %s: %w", s.Namespace, err)
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				return writeJSON(out, false, res)
			}
			info := res.GetNamespaceInfo()
			return table(out, [][]string{
				{"Name", info.GetName()},
				{"ID", info.GetId()},
				{"State", namespaceState(info)},
				{"Retention", duration(res.GetConfig().GetWorkflowExecutionRetentionTtl())},
				{"Description", info.GetDescription()},
			})
		},
	}
}

func (c *cli) namespaceListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the namespaces",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			client := workflowservice.NewWorkflowServiceClient(conn)
			var all []*workflowservice.DescribeNamespaceResponse
			var token []byte
			for {
				res, err := client.ListNamespaces(ctx, &workflowservice.ListNamespacesRequest{
					PageSize: 100, NextPageToken: token,
				})
				if err != nil {
					return fmt.Errorf("unable to list namespaces: %w", err)
				}
				all = append(all, res.GetNamespaces()...)
				if token = res.GetNextPageToken(); len(token) == 0 {
					break
				}
			}
			out := cmd.OutOrStdout()
			if c.output == outputJSON {
				msgs := make([]proto.Message, len(all))
				for i, ns := range all {
					msgs[i] = ns
				}
				return writeJSON(out, true, msgs...)
			}
			rows := [][]string{{"NAME", "STATE", "RETENTION", "DESCRIPTION"}}
			for _, ns := range all {
				info := ns.GetNamespaceInfo()
				rows = append(rows, []string{info.GetName(), namespaceState(info),
					duration(ns.GetConfig().GetWorkflowExecutionRetentionTtl()), info.GetDescription()})
			}
			return table(out, rows)
		},
	}
}

func (c *cli) namespaceUpdateCommand() *cobra.Command {
	var retention time.Duration
	var description string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Change a namespace's retention or description",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()
			if !flags.Changed("retention") && !flags.Changed("description") {
				return errors.New("nothing to update; set --retention or --description")
			}
			if flags.Changed("retention") && retention <= 0 {
				return errors.New("--retention must be positive")
			}
			// The engine ignores an empty description, so it cannot clear one; say so rather than report success.
			if flags.Changed("description") && description == "" {
				return errors.New("--description must not be empty: the engine keeps the current description")
			}
			conn, s, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			req := &workflowservice.UpdateNamespaceRequest{Namespace: s.Namespace}
			if flags.Changed("retention") {
				req.Config = &namespacepb.NamespaceConfig{WorkflowExecutionRetentionTtl: durationpb.New(retention)}
			}
			if flags.Changed("description") {
				req.UpdateInfo = &namespacepb.UpdateNamespaceInfo{Description: description}
			}
			if _, err := workflowservice.NewWorkflowServiceClient(conn).UpdateNamespace(ctx, req); err != nil {
				return fmt.Errorf("unable to update namespace %s: %w", s.Namespace, err)
			}
			return c.confirm(cmd, "Namespace %s updated.\n", s.Namespace)
		},
	}
	cmd.Flags().DurationVar(&retention, "retention", 0, "how long closed workflows are kept")
	cmd.Flags().StringVar(&description, "description", "", "a description of the namespace")
	return cmd
}

// namespaceState shows NAMESPACE_STATE_REGISTERED as Registered.
func namespaceState(info *namespacepb.NamespaceInfo) string {
	return enumName(info.GetState().String(), "NAMESPACE_STATE_")
}
