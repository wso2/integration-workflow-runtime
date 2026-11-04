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
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/server/api/adminservice/v1"
	clusterpb "go.temporal.io/server/api/cluster/v1"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// healthServices are the names each engine service reports its gRPC health under (Temporal's service packages).
var healthServices = map[string]string{
	"frontend":          "temporal.api.workflowservice.v1.WorkflowService",
	"internal-frontend": "temporal.api.workflowservice.v1.WorkflowService",
	"history":           "temporal.api.workflowservice.v1.HistoryService",
	"matching":          "temporal.api.workflowservice.v1.MatchingService",
	"worker":            "temporal.api.workflowservice.v1.WorkerService",
}

func (c *cli) clusterCommand() *cobra.Command {
	cluster := &cobra.Command{Use: "cluster", Short: "Check and describe the engine's nodes"}
	cluster.AddCommand(c.clusterHealthCommand(), c.clusterDescribeCommand())
	return cluster
}

func (c *cli) clusterHealthCommand() *cobra.Command {
	var service string
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check that a service is serving",
		Long: "Checks one service's gRPC health at --address: the frontend by default, or the service on that node's\n" +
			"port. Exits 1 when it is not serving, so it works as a container or load balancer health check.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, ok := healthServices[service]
			if !ok {
				return fmt.Errorf("--service %q must be one of %s", service, choices(healthServices))
			}
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			res, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{Service: name})
			if err != nil {
				return fmt.Errorf("unable to check %s: %w", service, err)
			}
			if c.output == outputJSON {
				if err := writeJSON(cmd.OutOrStdout(), false, res); err != nil {
					return err
				}
			} else {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), res.GetStatus().String())
			}
			if res.GetStatus() != healthpb.HealthCheckResponse_SERVING {
				return exitError(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&service, "service", "frontend",
		"frontend, internal-frontend, history, matching or worker")
	return cmd
}

func (c *cli) clusterDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "describe",
		Short: "Show the cluster and the nodes running each service",
		Long: "Shows the cluster's name, version, history shards and stores, and the nodes behind each service.\n" +
			"The node list needs a token with the system admin role; without it only the cluster is shown.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			conn, _, ctx, cancel, err := c.connect(cmd)
			if err != nil {
				return err
			}
			defer cancel()
			defer func() { _ = conn.Close() }()
			out := cmd.OutOrStdout()
			res, err := adminservice.NewAdminServiceClient(conn).DescribeCluster(ctx,
				&adminservice.DescribeClusterRequest{})
			if code := status.Code(err); code == codes.PermissionDenied || code == codes.Unimplemented {
				info, infoErr := workflowservice.NewWorkflowServiceClient(conn).GetClusterInfo(ctx,
					&workflowservice.GetClusterInfoRequest{})
				if infoErr != nil {
					return fmt.Errorf("unable to describe the cluster: %w", infoErr)
				}
				if c.output == outputJSON {
					return writeJSON(out, false, info)
				}
				if err := table(out, clusterRows(info.GetClusterName(), info.GetClusterId(), info.GetServerVersion(),
					info.GetHistoryShardCount(), info.GetPersistenceStore(), info.GetVisibilityStore())); err != nil {
					return err
				}
				_, err := fmt.Fprintln(out, "\nNodes are listed only for a token with the system admin role.")
				return err
			}
			if err != nil {
				return fmt.Errorf("unable to describe the cluster: %w", err)
			}
			if c.output == outputJSON {
				return writeJSON(out, false, res)
			}
			if err := table(out, clusterRows(res.GetClusterName(), res.GetClusterId(), res.GetServerVersion(),
				res.GetHistoryShardCount(), res.GetPersistenceStore(), res.GetVisibilityStore())); err != nil {
				return err
			}
			rows := [][]string{{"SERVICE", "NODES", "ADDRESSES"}}
			rings := res.GetMembershipInfo().GetRings()
			slices.SortFunc(rings, func(a, b *clusterRing) int { return strings.Compare(a.GetRole(), b.GetRole()) })
			for _, r := range rings {
				var hosts []string
				for _, m := range r.GetMembers() {
					hosts = append(hosts, m.GetIdentity())
				}
				slices.Sort(hosts)
				rows = append(rows, []string{r.GetRole(), strconv.Itoa(int(r.GetMemberCount())), strings.Join(hosts, ", ")})
			}
			_, _ = fmt.Fprintln(out)
			return table(out, rows)
		},
	}
}

type clusterRing = clusterpb.RingInfo

func clusterRows(name, id, version string, shards int32, persistence, visibility string) [][]string {
	return [][]string{
		{"Cluster", name},
		{"Cluster ID", id},
		{"Server version", version},
		{"History shards", strconv.Itoa(int(shards))},
		{"Persistence", persistence},
		{"Visibility", visibility},
	}
}
