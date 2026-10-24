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

final workflow:Duration resultTimeout = {seconds: 30};

@test:Config {groups: ["smoke"]}
function testWorkflowRunsActivities() returns error? {
    string id = check workflow:run(greetingWorkflow, {name: "runtime"});
    string result = check workflow:waitForResult(id, timeout = resultTimeout);
    test:assertEquals(result, "HELLO RUNTIME (7)");
    test:assertEquals(check workflow:getStatus(id), workflow:COMPLETED);
}

@test:Config {groups: ["smoke"]}
function testDataEventResumesWorkflow() returns error? {
    string id = check workflow:run(decisionWorkflow, {requestId: "req-1"});
    runtime:sleep(1);
    check workflow:sendData(decisionWorkflow, id, "decision", {approved: true, comment: "ok"});
    string result = check workflow:waitForResult(id, timeout = resultTimeout);
    test:assertEquals(result, "req-1:approved:ok");
}

@test:Config {groups: ["smoke"]}
function testFailingActivityFailsWorkflow() returns error? {
    string id = check workflow:run(failingWorkflow, {name: "bad"});
    string|error result = workflow:waitForResult(id, timeout = resultTimeout);
    test:assertTrue(result is error, "a failing activity must fail the workflow");
    test:assertEquals(check workflow:getStatus(id), workflow:FAILED);
}

@test:Config {groups: ["smoke"]}
function testConcurrentInstancesAreIndependent() returns error? {
    string first = check workflow:run(greetingWorkflow, {name: "a"});
    string second = check workflow:run(greetingWorkflow, {name: "bb"});
    test:assertNotEquals(first, second);
    string firstResult = check workflow:waitForResult(first, timeout = resultTimeout);
    string secondResult = check workflow:waitForResult(second, timeout = resultTimeout);
    test:assertEquals(firstResult, "HELLO A (1)");
    test:assertEquals(secondResult, "HELLO BB (2)");
}
