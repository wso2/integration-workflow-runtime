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

@test:Config {groups: ["full", "data-events"]}
function testDataEventsInDeclaredOrder() returns error? {
    string id = check workflow:run(orderWorkflow, {id: "o1"});
    runtime:sleep(1);
    check workflow:sendData(orderWorkflow, id, "approval", {approved: true, comment: "fine"});
    check workflow:sendData(orderWorkflow, id, "payment", {txnId: "txn-1", amount: 10.5d});
    string result = check workflow:waitForResult(id, timeout = resultTimeout);
    test:assertEquals(result, "o1:approved:txn-1");
}

@test:Config {groups: ["full", "data-events"]}
function testDataEventsInReverseOrder() returns error? {
    string id = check workflow:run(orderWorkflow, {id: "o2"});
    runtime:sleep(1);
    check workflow:sendData(orderWorkflow, id, "payment", {txnId: "txn-2", amount: 3d});
    check workflow:sendData(orderWorkflow, id, "approval", {approved: false, comment: "no"});
    string result = check workflow:waitForResult(id, timeout = resultTimeout);
    test:assertEquals(result, "o2:rejected:txn-2");
}
