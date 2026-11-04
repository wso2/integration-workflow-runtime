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

package enginetest

import (
	"context"
	"testing"
	"time"

	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// completeWorkflowTask answers one workflow task on the task queue with no commands, like a worker with nothing to
// do yet, so the workflow's history has a completed workflow task to reset to.
func completeWorkflowTask(t *testing.T, address, namespace, taskQueue string) {
	t.Helper()
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client := workflowservice.NewWorkflowServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	task, err := client.PollWorkflowTaskQueue(ctx, &workflowservice.PollWorkflowTaskQueueRequest{
		Namespace: namespace, TaskQueue: &taskqueuepb.TaskQueue{Name: taskQueue}, Identity: "enginetest",
	})
	if err != nil || len(task.GetTaskToken()) == 0 {
		t.Fatalf("no workflow task on %s: %v", taskQueue, err)
	}
	if _, err := client.RespondWorkflowTaskCompleted(ctx, &workflowservice.RespondWorkflowTaskCompletedRequest{
		Namespace: namespace, TaskToken: task.GetTaskToken(), Identity: "enginetest",
	}); err != nil {
		t.Fatalf("unable to complete the workflow task: %v", err)
	}
}
