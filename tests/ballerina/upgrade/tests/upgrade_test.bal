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

// The runner runs the "before" group on the old engine and the "after" group on the upgraded one.
const ORDER_ID = "upgrade-order-1";

final workflow:Duration resultTimeout = {seconds: 60};

@test:Config {groups: ["before"]}
function testStartOnTheOldEngine() returns error? {
    _ = check workflow:runWithId(orderWorkflow, ORDER_ID, {orderId: ORDER_ID});
    // Gives the worker time to run the first activity; the runner checks the history before it stops the engine.
    runtime:sleep(3);
    test:assertEquals(check workflow:getStatus(ORDER_ID), workflow:RUNNING);
}

@test:Config {groups: ["after"]}
function testFinishOnTheUpgradedEngine() returns error? {
    test:assertEquals(check workflow:getStatus(ORDER_ID), workflow:RUNNING);
    check workflow:sendData(orderWorkflow, ORDER_ID, "decision", {approved: true, comment: "approved"});
    string result = check workflow:waitForResult(ORDER_ID, timeout = resultTimeout);
    test:assertEquals(result, "reserved upgrade-order-1; approved; shipped upgrade-order-1");
    test:assertEquals(check workflow:getStatus(ORDER_ID), workflow:COMPLETED);
}
