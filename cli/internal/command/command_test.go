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
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	namespacepb "go.temporal.io/api/namespace/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/server/api/adminservice/v1"
	clusterpb "go.temporal.io/server/api/cluster/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/wso2/integration-workflow-runtime/cli/internal/clientconfig"
)

// fakeEngine answers the calls workflow-cli makes and records the authorization header of each.
type fakeEngine struct {
	mu          sync.Mutex
	auth        []string
	namespaces  map[string]*workflowservice.DescribeNamespaceResponse
	attributes  map[string]map[string]enumspb.IndexedValueType
	notYetSeen  int // AddSearchAttributes answers NotFound this many times, as for a namespace not yet cached
	adminDenied bool
	health      *health.Server
}

// One type per service: the generated Unimplemented servers share method names.
type (
	workflowFake struct {
		workflowservice.UnimplementedWorkflowServiceServer
		*fakeEngine
	}
	operatorFake struct {
		operatorservice.UnimplementedOperatorServiceServer
		*fakeEngine
	}
	adminFake struct {
		adminservice.UnimplementedAdminServiceServer
		*fakeEngine
	}
)

func newFakeEngine() *fakeEngine {
	f := &fakeEngine{
		namespaces: map[string]*workflowservice.DescribeNamespaceResponse{},
		attributes: map[string]map[string]enumspb.IndexedValueType{},
		health:     health.NewServer(),
	}
	f.health.SetServingStatus(healthServices["frontend"], healthpb.HealthCheckResponse_SERVING)
	f.health.SetServingStatus(healthServices["history"], healthpb.HealthCheckResponse_NOT_SERVING)
	return f
}

func (f *fakeEngine) record(ctx context.Context) {
	md, _ := metadata.FromIncomingContext(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, strings.Join(md.Get("authorization"), ","))
}

func (f workflowFake) RegisterNamespace(ctx context.Context, r *workflowservice.RegisterNamespaceRequest) (
	*workflowservice.RegisterNamespaceResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.namespaces[r.GetNamespace()]; ok {
		return nil, serviceerror.NewNamespaceAlreadyExists("Namespace already exists.")
	}
	f.namespaces[r.GetNamespace()] = &workflowservice.DescribeNamespaceResponse{
		NamespaceInfo: &namespacepb.NamespaceInfo{Name: r.GetNamespace(), Id: "id-" + r.GetNamespace(),
			Description: r.GetDescription(), State: enumspb.NAMESPACE_STATE_REGISTERED},
		Config: &namespacepb.NamespaceConfig{WorkflowExecutionRetentionTtl: r.GetWorkflowExecutionRetentionPeriod()},
	}
	return &workflowservice.RegisterNamespaceResponse{}, nil
}

func (f workflowFake) DescribeNamespace(ctx context.Context, r *workflowservice.DescribeNamespaceRequest) (
	*workflowservice.DescribeNamespaceResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if ns, ok := f.namespaces[r.GetNamespace()]; ok {
		return ns, nil
	}
	return nil, serviceerror.NewNamespaceNotFound(r.GetNamespace())
}

// ListNamespaces returns one namespace per page, to exercise paging.
func (f workflowFake) ListNamespaces(ctx context.Context, r *workflowservice.ListNamespacesRequest) (
	*workflowservice.ListNamespacesResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.namespaces))
	for n := range f.namespaces {
		names = append(names, n)
	}
	slices.Sort(names)
	i := 0
	if len(r.GetNextPageToken()) > 0 {
		i = int(r.GetNextPageToken()[0])
	}
	res := &workflowservice.ListNamespacesResponse{}
	if i < len(names) {
		res.Namespaces = []*workflowservice.DescribeNamespaceResponse{f.namespaces[names[i]]}
		if i+1 < len(names) {
			res.NextPageToken = []byte{byte(i + 1)}
		}
	}
	return res, nil
}

func (f workflowFake) UpdateNamespace(ctx context.Context, r *workflowservice.UpdateNamespaceRequest) (
	*workflowservice.UpdateNamespaceResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	ns, ok := f.namespaces[r.GetNamespace()]
	if !ok {
		return nil, serviceerror.NewNamespaceNotFound(r.GetNamespace())
	}
	if r.GetConfig() != nil {
		ns.Config.WorkflowExecutionRetentionTtl = r.GetConfig().GetWorkflowExecutionRetentionTtl()
	}
	// As Temporal's namespace handler: an empty description leaves the current one.
	if d := r.GetUpdateInfo().GetDescription(); d != "" {
		ns.NamespaceInfo.Description = d
	}
	return &workflowservice.UpdateNamespaceResponse{}, nil
}

func (f workflowFake) GetClusterInfo(ctx context.Context, _ *workflowservice.GetClusterInfoRequest) (
	*workflowservice.GetClusterInfoResponse, error) {
	f.record(ctx)
	return &workflowservice.GetClusterInfoResponse{ClusterName: "active", ClusterId: "c-1", ServerVersion: "1.32.1",
		HistoryShardCount: 512, PersistenceStore: "postgres12", VisibilityStore: "postgres12"}, nil
}

func (f operatorFake) AddSearchAttributes(ctx context.Context, r *operatorservice.AddSearchAttributesRequest) (
	*operatorservice.AddSearchAttributesResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notYetSeen > 0 {
		f.notYetSeen--
		return nil, serviceerror.NewNamespaceNotFound(r.GetNamespace())
	}
	if f.attributes[r.GetNamespace()] == nil {
		f.attributes[r.GetNamespace()] = map[string]enumspb.IndexedValueType{}
	}
	for k, v := range r.GetSearchAttributes() {
		f.attributes[r.GetNamespace()][k] = v
	}
	return &operatorservice.AddSearchAttributesResponse{}, nil
}

func (f operatorFake) ListSearchAttributes(ctx context.Context, r *operatorservice.ListSearchAttributesRequest) (
	*operatorservice.ListSearchAttributesResponse, error) {
	f.record(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	return &operatorservice.ListSearchAttributesResponse{
		CustomAttributes: f.attributes[r.GetNamespace()],
		SystemAttributes: map[string]enumspb.IndexedValueType{"WorkflowId": enumspb.INDEXED_VALUE_TYPE_KEYWORD},
	}, nil
}

func (f adminFake) DescribeCluster(ctx context.Context, _ *adminservice.DescribeClusterRequest) (
	*adminservice.DescribeClusterResponse, error) {
	f.record(ctx)
	if f.adminDenied {
		return nil, status.Error(codes.PermissionDenied, "Request unauthorized.")
	}
	return &adminservice.DescribeClusterResponse{ClusterName: "active", ClusterId: "c-1", ServerVersion: "1.32.1",
		HistoryShardCount: 512, PersistenceStore: "postgres12", VisibilityStore: "postgres12",
		MembershipInfo: &clusterpb.MembershipInfo{Rings: []*clusterpb.RingInfo{
			{Role: "history", MemberCount: 2, Members: []*clusterpb.HostInfo{{Identity: "10.0.0.22:7234"},
				{Identity: "10.0.0.21:7234"}}},
			{Role: "frontend", MemberCount: 1, Members: []*clusterpb.HostInfo{{Identity: "10.0.0.11:7233"}}},
		}}}, nil
}

// testCLI runs workflow-cli against the fake engine, with a profile file in a temporary directory.
type testCLI struct {
	t      *testing.T
	engine *fakeEngine
	lis    *bufconn.Listener
	env    map[string]string
}

func newTestCLI(t *testing.T) *testCLI {
	t.Helper()
	f := newFakeEngine()
	lis := bufconn.Listen(1 << 20)
	// Temporal's server turns its service errors into gRPC statuses in an interceptor; so does this fake.
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		res, err := handler(ctx, req)
		if se, ok := err.(serviceerror.ServiceError); ok {
			return res, se.Status().Err()
		}
		return res, err
	}))
	workflowservice.RegisterWorkflowServiceServer(srv, workflowFake{fakeEngine: f})
	operatorservice.RegisterOperatorServiceServer(srv, operatorFake{fakeEngine: f})
	adminservice.RegisterAdminServiceServer(srv, adminFake{fakeEngine: f})
	healthpb.RegisterHealthServer(srv, f.health)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return &testCLI{t: t, engine: f, lis: lis,
		env: map[string]string{"WORKFLOW_CONFIG_FILE": filepath.Join(t.TempDir(), "cli.toml")}}
}

func (tc *testCLI) run(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	c := &cli{version: "test", stdout: &out, stderr: &errOut,
		lookupEnv: func(k string) (string, bool) { v, ok := tc.env[k]; return v, ok },
		dial: func(s clientconfig.Settings) (*grpc.ClientConn, error) {
			opts, err := dialOptions(s)
			if err != nil {
				return nil, err
			}
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
					return tc.lis.DialContext(ctx)
				}))
			return grpc.NewClient("passthrough:///bufnet", opts...)
		}}
	code = c.run(args)
	return code, out.String(), errOut.String()
}

func (tc *testCLI) must(args ...string) string {
	tc.t.Helper()
	code, out, errOut := tc.run(args...)
	if code != 0 {
		tc.t.Fatalf("workflow-cli %s: exit %d\n%s%s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func TestVersion(t *testing.T) {
	if out := newTestCLI(t).must("version"); out != "workflow-cli test\n" {
		t.Fatalf("version = %q", out)
	}
}

func TestClusterHealth(t *testing.T) {
	tc := newTestCLI(t)
	if out := tc.must("operator", "cluster", "health"); out != "SERVING\n" {
		t.Fatalf("frontend health = %q", out)
	}
	if code, out, _ := tc.run("operator", "cluster", "health", "--service", "history"); code != 1 ||
		out != "NOT_SERVING\n" {
		t.Fatalf("history health = %d %q, want 1 NOT_SERVING", code, out)
	}
	if code, _, errOut := tc.run("operator", "cluster", "health", "--service", "manager"); code != 1 ||
		!strings.Contains(errOut, "must be one of frontend, history") {
		t.Fatalf("unknown service = %d %q", code, errOut)
	}
	if code, _, errOut := tc.run("operator", "cluster", "health", "--service", "matching"); code != 1 ||
		!strings.Contains(errOut, "unable to check matching") {
		t.Fatalf("unregistered service = %d %q", code, errOut)
	}
	if out := tc.must("operator", "cluster", "health", "-o", "json"); !strings.Contains(out, `"status": "SERVING"`) {
		t.Fatalf("json health = %q", out)
	}
}

func TestClusterDescribe(t *testing.T) {
	tc := newTestCLI(t)
	out := tc.must("operator", "cluster", "describe")
	for _, want := range []string{"History shards  512", "frontend  1      10.0.0.11:7233",
		"history   2      10.0.0.21:7234, 10.0.0.22:7234"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "frontend") > strings.Index(out, "history  ") {
		t.Errorf("services are not sorted:\n%s", out)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(tc.must("operator", "cluster", "describe", "-o", "json")), &doc); err != nil ||
		doc["clusterName"] != "active" {
		t.Fatalf("json describe = %v, %v", doc, err)
	}

	tc.engine.adminDenied = true
	out = tc.must("operator", "cluster", "describe")
	if !strings.Contains(out, "Server version  1.32.1") || !strings.Contains(out, "system admin role") ||
		strings.Contains(out, "ADDRESSES") {
		t.Fatalf("describe without admin:\n%s", out)
	}
}

func TestNamespaceLifecycle(t *testing.T) {
	tc := newTestCLI(t)
	tc.engine.notYetSeen = 2
	out := tc.must("operator", "namespace", "create", "-n", "orders", "--description", "Orders")
	if !strings.Contains(out, "Namespace orders created.") || !strings.Contains(out, "WorkflowKind") {
		t.Fatalf("create:\n%s", out)
	}
	if got := tc.engine.attributes["orders"][workflowKind]; got != enumspb.INDEXED_VALUE_TYPE_KEYWORD {
		t.Fatalf("WorkflowKind = %v after the namespace reached the cache", got)
	}
	if out := tc.must("operator", "namespace", "create", "-n", "orders"); !strings.Contains(out, "already exists") {
		t.Fatalf("second create:\n%s", out)
	}
	tc.must("operator", "namespace", "create", "-n", "billing", "--retention", "72h")
	// With -o json a change reports on stderr, so stdout holds only JSON, here none.
	if code, out, errOut := tc.run("operator", "namespace", "update", "-n", "billing", "--retention", "72h", "-o",
		"json"); code != 0 || out != "" || !strings.Contains(errOut, "Namespace billing updated.") {
		t.Fatalf("json update: exit %d, stdout %q, stderr %q", code, out, errOut)
	}

	out = tc.must("operator", "namespace", "describe", "-n", "orders")
	for _, want := range []string{"Name         orders", "State        Registered", "Retention    30d",
		"Description  Orders"} {
		if !strings.Contains(out, want) {
			t.Errorf("describe lacks %q:\n%s", want, out)
		}
	}
	out = tc.must("operator", "namespace", "list")
	if !strings.Contains(out, "billing  Registered  3d") || !strings.Contains(out, "orders   Registered  30d") {
		t.Fatalf("list over two pages:\n%s", out)
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(tc.must("operator", "namespace", "list", "-o", "json")), &list); err != nil ||
		len(list) != 2 {
		t.Fatalf("json list = %v, %v", list, err)
	}

	tc.must("operator", "namespace", "update", "-n", "orders", "--retention", "36h", "--description", "Order processing")
	ns := tc.engine.namespaces["orders"]
	if ns.GetConfig().GetWorkflowExecutionRetentionTtl().AsDuration() != 36*time.Hour ||
		ns.GetNamespaceInfo().GetDescription() != "Order processing" {
		t.Fatalf("after update: %v", ns)
	}
	for args, want := range map[string]string{
		"operator namespace update -n orders":                  "nothing to update",
		"operator namespace update -n orders --retention 0s":   "must be positive",
		"operator namespace update -n orders --description=":   "must not be empty",
		"operator namespace create -n x --retention -1h":       "must be positive",
		"operator namespace describe -n missing":               "unable to describe namespace missing",
		"operator namespace update -n missing --description x": "unable to update namespace missing",
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
}

func TestSearchAttributes(t *testing.T) {
	tc := newTestCLI(t)
	tc.must("operator", "namespace", "create", "-n", "orders")
	tc.must("operator", "search-attribute", "create", "-n", "orders", "--name", "Customer", "--type", "Keyword",
		"--name", "Amount", "--type", "Double")
	out := tc.must("operator", "search-attribute", "list", "-n", "orders")
	for _, want := range []string{"Amount        Double   custom", "Customer      Keyword  custom",
		"WorkflowKind  Keyword  custom", "WorkflowId    Keyword  system"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	for args, want := range map[string]string{
		"operator search-attribute create -n orders --name A":            "give each --name a --type",
		"operator search-attribute create -n orders --name A --type Big": `--type "Big" must be one of Bool`,
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
}

func TestProfilesEnvironmentAndFlags(t *testing.T) {
	tc := newTestCLI(t)
	tc.must("config", "set", "--prop", "namespace", "--value", "from-profile")
	tc.must("config", "set", "--prop", "apiKey", "--value", "profile-token")
	tc.must("config", "set", "--profile", "prod", "--prop", "namespace", "--value", "prod-ns")
	tc.must("operator", "namespace", "create")
	tc.must("operator", "namespace", "create", "--profile", "prod")
	tc.env["WORKFLOW_NAMESPACE"] = "from-env"
	tc.env["WORKFLOW_API_KEY"] = "env-token"
	tc.must("operator", "namespace", "create")
	tc.must("operator", "namespace", "create", "-n", "from-flag", "--api-key", "flag-token")
	for _, ns := range []string{"from-profile", "prod-ns", "from-env", "from-flag"} {
		if _, ok := tc.engine.namespaces[ns]; !ok {
			t.Errorf("namespace %s was not created; have %v", ns, tc.engine.namespaces)
		}
	}
	if got := tc.engine.auth; got[0] != "Bearer profile-token" || got[len(got)-1] != "Bearer flag-token" ||
		!slices.Contains(got, "Bearer env-token") {
		t.Fatalf("authorization headers = %v", got)
	}

	if out := tc.must("config", "list"); out != "default\nprod\n" {
		t.Fatalf("config list = %q", out)
	}
	if out := tc.must("config", "get"); !strings.Contains(out, "apiKey     (set)") ||
		strings.Contains(out, "profile-token") {
		t.Fatalf("config get shows the key:\n%s", out)
	}
	if out := tc.must("config", "get", "--profile", "prod", "--prop", "namespace"); out != "prod-ns\n" {
		t.Fatalf("config get --prop = %q", out)
	}
	tc.must("config", "delete", "--prop", "apiKey")
	if out := tc.must("config", "get", "--prop", "apiKey"); out != "\n" {
		t.Fatalf("apiKey after delete = %q", out)
	}
	tc.must("config", "delete-profile", "--profile", "prod")
	if out := tc.must("config", "list"); out != "default\n" {
		t.Fatalf("config list after delete-profile = %q", out)
	}
	info, err := os.Stat(tc.env["WORKFLOW_CONFIG_FILE"])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("profile file mode = %v, %v; want 0600", info, err)
	}
	for args, want := range map[string]string{
		"operator namespace list --profile missing":  `no profile "missing"`,
		"config get --profile missing":               `no profile "missing"`,
		"config set --prop colour --value red":       `unknown property "colour"`,
		"config set --prop tls.enabled --value yes2": "must be true or false",
		"config set --prop address":                  "set both --prop and --value",
		"config delete":                              "delete-profile removes",
		"config delete-profile --profile missing":    `no profile "missing"`,
		"operator namespace list -o yaml":            `--output "yaml" must be text or json`,
	} {
		if code, _, errOut := tc.run(strings.Fields(args)...); code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s = %d %q, want %q", args, code, errOut, want)
		}
	}
}

func TestRetentionDisplay(t *testing.T) {
	for d, want := range map[time.Duration]string{720 * time.Hour: "30d", 36 * time.Hour: "36h0m0s"} {
		if got := duration(durationpb.New(d)); got != want {
			t.Errorf("duration(%s) = %q, want %q", d, got, want)
		}
	}
	if duration(nil) != "" {
		t.Error("a missing retention is not empty")
	}
}
