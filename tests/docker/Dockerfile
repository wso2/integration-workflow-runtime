# Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Toolchain for running the CI gates and the Ballerina suites, matching the GitHub Actions jobs.
ARG GO_VERSION=1.26.10

FROM eclipse-temurin:25-jre AS jre

FROM golang:${GO_VERSION}-bookworm
ARG BALLERINA_VERSION=2201.14.1
ARG TEMPORAL_CLI_VERSION=1.9.1
ARG TARGETARCH

RUN apt-get update && apt-get install -y --no-install-recommends zip unzip \
    && rm -rf /var/lib/apt/lists/*

# The platform-independent Ballerina zip has no JRE; Ballerina Swan Lake Update 14 runs on Java 25.
COPY --from=jre /opt/java/openjdk /opt/java/openjdk
ENV JAVA_HOME=/opt/java/openjdk
RUN curl -fsSL -o /tmp/bal.zip \
        "https://dist.ballerina.io/downloads/${BALLERINA_VERSION}/ballerina-${BALLERINA_VERSION}-swan-lake.zip" \
    && unzip -q /tmp/bal.zip -d /opt && mv "/opt/ballerina-${BALLERINA_VERSION}-swan-lake" /opt/ballerina \
    && rm /tmp/bal.zip
ENV PATH=/opt/ballerina/bin:$PATH

RUN curl -fsSL "https://github.com/temporalio/cli/releases/download/v${TEMPORAL_CLI_VERSION}/temporal_cli_${TEMPORAL_CLI_VERSION}_linux_${TARGETARCH}.tar.gz" \
        | tar -xz -C /usr/local/bin temporal

COPY run.sh /usr/local/bin/run-tests
ENTRYPOINT ["run-tests"]
