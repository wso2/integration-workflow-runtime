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

@test:Config {groups: ["full", "activities"]}
function testActivityRetriesUntilSuccess() returns error? {
    string id = check workflow:run(retryWorkflow, {id: uniqueKey("retry")});
    string result = check workflow:waitForResult(id, timeout = resultTimeout);
    test:assertEquals(result, "succeeded on attempt 3");
}

@test:Config {groups: ["full", "errors"]}
function testWorkflowErrorFailsInstance() returns error? {
    string id = check workflow:run(explicitErrorWorkflow, {id: "req-42"});
    string|error result = workflow:waitForResult(id, timeout = resultTimeout);
    if result is string {
        test:assertFail("an error returned by the workflow must fail the instance");
    }
    test:assertTrue(result.message().includes("validation failed for req-42"), result.message());
    test:assertEquals(check workflow:getStatus(id), workflow:FAILED);
}
