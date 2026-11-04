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

import ballerina/workflow;

type OrderInput record {|
    string orderId;
|};

type Decision record {|
    boolean approved;
    string comment;
|};

@workflow:Activity
function reserveStock(string orderId) returns string|error {
    return "reserved " + orderId;
}

@workflow:Activity
function shipOrder(string orderId) returns string|error {
    return "shipped " + orderId;
}

// Reserves stock on the old engine, waits there for the decision, and ships on the upgraded engine.
@workflow:Workflow
function orderWorkflow(workflow:Context ctx, OrderInput input, record {|future<Decision> decision;|} events)
        returns string|error {
    string reserved = check ctx->callActivity(reserveStock, {"orderId": input.orderId});
    Decision decision = check wait events.decision;
    if !decision.approved {
        return string `${reserved}; rejected: ${decision.comment}`;
    }
    string shipped = check ctx->callActivity(shipOrder, {"orderId": input.orderId});
    return string `${reserved}; ${decision.comment}; ${shipped}`;
}
