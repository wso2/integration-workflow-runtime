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

import ballerina/lang.runtime;
import ballerina/test;
import ballerina/workflow;
import ballerina/workflow.management;

isolated function waitForPendingTask(string parentId) returns string|error {
    foreach int _ in 0 ..< 30 {
        management:HumanTaskGroup[] groups = check management:listPendingHumanTasks(parentId);
        if groups.length() > 0 && groups[0].taskIds.length() > 0 {
            return groups[0].taskIds[0];
        }
        runtime:sleep(1);
    }
    return error("no pending human task for " + parentId);
}

@test:Config {groups: ["full", "human-tasks"]}
function testHumanTaskCompletionResumesWorkflow() returns error? {
    string id = check workflow:run(humanTaskWorkflow, {id: "h1"});
    string taskId = check waitForPendingTask(id);
    check workflow:completeHumanTask(taskId, {approved: true, comment: "LGTM"});
    string result = check workflow:waitForResult(id, timeout = resultTimeout);
    test:assertEquals(result, "approved:LGTM");
}

@test:Config {groups: ["full", "human-tasks"]}
function testHumanTaskRejection() returns error? {
    string id = check workflow:run(humanTaskWorkflow, {id: "h2"});
    string taskId = check waitForPendingTask(id);
    check workflow:completeHumanTask(taskId, {approved: false, comment: "too high"});
    string result = check workflow:waitForResult(id, timeout = resultTimeout);
    test:assertEquals(result, "rejected:too high");
}
