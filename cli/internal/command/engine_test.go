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
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"

	"github.com/wso2/integration-workflow-runtime/cli/internal/clientconfig"
)

// devEngine starts `workflow-engine start-dev` from WORKFLOW_ENGINE on free ports, or skips the test. The engine tests
// in tools/internal/enginetest cover the other commands; this one needs a worker, which takes the Temporal API.
func devEngine(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("WORKFLOW_ENGINE")
	if binary == "" {
		t.Skip("WORKFLOW_ENGINE does not name the engine binary")
	}
	port := freePorts(t)
	var log bytes.Buffer
	cmd := exec.Command(binary, "start-dev", "--port", strconv.Itoa(port)) //nolint:gosec // the engine under test
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(time.Second) {
		if code, out, _ := realCLI(t, "operator", "cluster", "health", "--address", address); code == 0 &&
			strings.Contains(out, "SERVING") {
			return address
		}
	}
	t.Fatalf("the development engine did not report SERVING:\n%s", log.String())
	return ""
}

// freePorts finds a frontend port whose service ports (+1 to +10) and membership ports (-300) are free.
func freePorts(t *testing.T) int {
	t.Helper()
	for base := 23000 + os.Getpid()%4000; base < 32000; base += 20 {
		free := true
		for _, offset := range []int{0, 1, 2, 3, 6, 10, -300, -299, -298, -297, -294} {
			l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", base+offset))
			if err != nil {
				free = false
				break
			}
			_ = l.Close()
		}
		if free {
			return base
		}
	}
	t.Fatal("no free port range")
	return 0
}

// realCLI runs workflow-cli with the real dialer and a profile file of its own.
func realCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "cli.toml")
	var out, errOut bytes.Buffer
	c := &cli{version: "test", stdout: &out, stderr: &errOut, dial: dial,
		lookupEnv: func(k string) (string, bool) {
			if k == "WORKFLOW_CONFIG_FILE" {
				return config, true
			}
			return "", false
		}}
	code := c.run(args)
	return code, out.String(), errOut.String()
}

func TestResetOnARealEngine(t *testing.T) {
	address := devEngine(t)
	conn, err := dial(clientconfig.Settings{Address: address})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := workflowservice.NewWorkflowServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	queue := &taskqueuepb.TaskQueue{Name: "resets"}
	started, err := client.StartWorkflowExecution(ctx, &workflowservice.StartWorkflowExecutionRequest{
		Namespace: "default", WorkflowId: "order-reset", WorkflowType: &commonpb.WorkflowType{Name: "Order"},
		TaskQueue: queue, RequestId: requestID(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// A worker with nothing to do yet: it answers the first workflow task with no commands.
	task, err := client.PollWorkflowTaskQueue(ctx, &workflowservice.PollWorkflowTaskQueueRequest{
		Namespace: "default", TaskQueue: queue, Identity: "reset-test"})
	if err != nil || len(task.GetTaskToken()) == 0 {
		t.Fatalf("no workflow task: %v", err)
	}
	if _, err := client.RespondWorkflowTaskCompleted(ctx, &workflowservice.RespondWorkflowTaskCompletedRequest{
		Namespace: "default", TaskToken: task.GetTaskToken(), Identity: "reset-test"}); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := realCLI(t, "workflow", "reset", "-w", "order-reset", "--type", "LastWorkflowTask",
		"--reason", "replay", "--address", address)
	if code != 0 || !strings.Contains(out, "the new run is") {
		t.Fatalf("reset = %d\n%s%s", code, out, errOut)
	}
	res, err := client.DescribeWorkflowExecution(ctx, &workflowservice.DescribeWorkflowExecutionRequest{
		Namespace: "default", Execution: &commonpb.WorkflowExecution{WorkflowId: "order-reset"}})
	if err != nil {
		t.Fatal(err)
	}
	info := res.GetWorkflowExecutionInfo()
	if run := info.GetExecution().GetRunId(); run == started.GetRunId() || !strings.Contains(out, run) ||
		info.GetStatus().String() != "Running" {
		t.Fatalf("after reset: run %s (%s), want a new running run besides %s\n%s", run, info.GetStatus(),
			started.GetRunId(), out)
	}
}
