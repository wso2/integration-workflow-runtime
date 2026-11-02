# One image for every profile, from the linux zips `make dist` builds: the image ships the same binaries, SBOMs and
# licenses as the zip. The profiles and the other settings come from a mounted conf/runtime.toml.
#   make image VERSION=<v>   (builds the linux/amd64 and linux/arm64 zips first)
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETARCH
ARG VERSION
WORKDIR /opt/wso2-workflow-runtime
COPY dist/wso2-workflow-runtime-${VERSION}-linux-${TARGETARCH}/bin/workflow-engine \
     dist/wso2-workflow-runtime-${VERSION}-linux-${TARGETARCH}/bin/workflow-engine-sql bin/
COPY dist/wso2-workflow-runtime-${VERSION}-linux-${TARGETARCH}/conf/ conf/
COPY dist/wso2-workflow-runtime-${VERSION}-linux-${TARGETARCH}/sbom/ sbom/
COPY dist/wso2-workflow-runtime-${VERSION}-linux-${TARGETARCH}/third-party/ third-party/
COPY dist/wso2-workflow-runtime-${VERSION}-linux-${TARGETARCH}/LICENSE \
     dist/wso2-workflow-runtime-${VERSION}-linux-${TARGETARCH}/NOTICE ./
USER nonroot
# Frontend gRPC and HTTP; the other service and membership ports matter only between nodes.
EXPOSE 7233 7243
ENTRYPOINT ["/opt/wso2-workflow-runtime/bin/workflow-engine", "start", "--runtime-config", "/opt/wso2-workflow-runtime/conf/runtime.toml"]
