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

// Activities and workflows shared by the full suite, grouped by the feature each test file covers.

type Request record {|
    string id;
|};

type Approval record {|
    boolean approved;
    string comment;
|};

type Payment record {|
    string txnId;
    decimal amount;
|};

isolated map<int> attemptsByKey = {};

@workflow:Activity
function flakyActivity(string key) returns string|error {
    int attempt;
    lock {
        attempt = (attemptsByKey[key] ?: 0) + 1;
        attemptsByKey[key] = attempt;
    }
    if attempt < 3 {
        return error(string `attempt ${attempt} failed`);
    }
    return string `succeeded on attempt ${attempt}`;
}

@workflow:Activity
function echoActivity(string text) returns string|error {
    return text;
}

@workflow:Workflow
function retryWorkflow(workflow:Context ctx, Request input) returns string|error {
    return ctx->callActivity(flakyActivity, {"key": input.id},
            retryPolicy = <workflow:AutoRetry>{maxRetries: 3, retryDelay: 0.1d});
}

@workflow:Workflow
function timerWorkflow(workflow:Context ctx, Request input) returns string|error {
    check ctx.sleep({seconds: 2});
    return input.id + ":woke";
}

@workflow:Workflow
function childWorkflow(workflow:Context ctx, Request input) returns string|error {
    return ctx->callActivity(echoActivity, {"text": "child-" + input.id});
}

@workflow:Workflow
function parentStartsAndWaitsWorkflow(workflow:Context ctx, Request input) returns string|error {
    string childId = check ctx->runChildWorkflow(childWorkflow, {id: input.id});
    string childResult = check ctx->waitForChildWorkflow(childId);
    return "parent+" + childResult;
}

@workflow:Workflow
function parentCallsWorkflow(workflow:Context ctx, Request input) returns string|error {
    string childResult = check ctx->callWorkflow(childWorkflow, {id: input.id});
    return "called+" + childResult;
}

@workflow:Workflow
function orderWorkflow(workflow:Context ctx, Request input,
        record {|future<Approval> approval; future<Payment> payment;|} events) returns string|error {
    Approval approval = check wait events.approval;
    Payment payment = check wait events.payment;
    return string `${input.id}:${approval.approved ? "approved" : "rejected"}:${payment.txnId}`;
}

@workflow:Workflow
function humanTaskWorkflow(workflow:Context ctx, Request input) returns string|error {
    Approval decision = check ctx->awaitHumanTask("approveRequest", {requestId: input.id},
            userRoles = ["APPROVER"], title = "Approve " + input.id);
    return decision.approved ? "approved:" + decision.comment : "rejected:" + decision.comment;
}

@workflow:Workflow
function explicitErrorWorkflow(workflow:Context ctx, Request input) returns string|error {
    string echoed = check ctx->callActivity(echoActivity, {"text": input.id});
    return error("validation failed for " + echoed);
}
