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
import ballerina/time;
import ballerina/workflow;

@test:Config {groups: ["full", "timers"]}
function testDurableTimerSuspendsAndResumes() returns error? {
    time:Utc started = time:utcNow();
    string id = check workflow:run(timerWorkflow, {id: "t1"});
    string result = check workflow:waitForResult(id, timeout = resultTimeout);
    test:assertEquals(result, "t1:woke");
    test:assertTrue(time:utcDiffSeconds(time:utcNow(), started) >= 2d, "the timer must wait at least two seconds");
}
