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

import ballerina/test;
import ballerina/workflow;
import ballerina/workflow.management;

@test:Config {groups: ["full", "management"]}
function testExecutionInfoListsActivities() returns error? {
    string id = check workflow:run(parentCallsWorkflow, {id: "m1"});
    string _ = check workflow:waitForResult(id, timeout = resultTimeout);
    management:WorkflowExecutionInfo info = check management:getWorkflowInfo(id);
    test:assertEquals(info.status, "COMPLETED");
}

@test:Config {groups: ["full", "management"]}
function testActivityHistoryRecordsRetries() returns error? {
    string id = check workflow:run(retryWorkflow, {id: uniqueKey("history")});
    string _ = check workflow:waitForResult(id, timeout = resultTimeout);
    management:WorkflowExecutionInfo info = check management:getWorkflowInfo(id);
    management:ActivityInvocation[] flaky = from management:ActivityInvocation inv in info.activityInvocations
        where inv.activityName.includes("flakyActivity")
        select inv;
    test:assertEquals(flaky.length(), 1, "one logical activity call");
    int? attempt = flaky[0].attempt;
    test:assertTrue(attempt is int && attempt >= 3, string `attempt ${attempt.toString()} should be at least 3`);
}
