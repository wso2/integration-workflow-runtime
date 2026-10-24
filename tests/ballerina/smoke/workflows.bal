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

type GreetingInput record {|
    string name;
|};

type DecisionInput record {|
    string requestId;
|};

type Decision record {|
    boolean approved;
    string comment;
|};

@workflow:Activity
function toUpper(string text) returns string|error {
    return text.toUpperAscii();
}

@workflow:Activity
function countCharacters(string text) returns int|error {
    return text.length();
}

@workflow:Activity
function rejectInput(string text) returns string|error {
    return error("rejected: " + text);
}

@workflow:Workflow
function greetingWorkflow(workflow:Context ctx, GreetingInput input) returns string|error {
    string upper = check ctx->callActivity(toUpper, {"text": input.name});
    int length = check ctx->callActivity(countCharacters, {"text": input.name});
    return string `HELLO ${upper} (${length})`;
}

@workflow:Workflow
function decisionWorkflow(workflow:Context ctx, DecisionInput input, record {|future<Decision> decision;|} events)
        returns string|error {
    Decision decision = check wait events.decision;
    return string `${input.requestId}:${decision.approved ? "approved" : "rejected"}:${decision.comment}`;
}

@workflow:Workflow
function failingWorkflow(workflow:Context ctx, GreetingInput input) returns string|error {
    return ctx->callActivity(rejectInput, {"text": input.name});
}
