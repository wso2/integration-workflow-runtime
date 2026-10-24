# Sketch: one image, the profile chosen at start (WORKFLOW_RUNTIME_PROFILES).
FROM gcr.io/distroless/static-debian12
COPY build/workflow-engine build/workflow-engine-sql /opt/workflow-runtime/bin/
COPY distribution/conf/ /opt/workflow-runtime/conf/
COPY LICENSE NOTICE /opt/workflow-runtime/
COPY third-party/ /opt/workflow-runtime/third-party/
ENTRYPOINT ["/opt/workflow-runtime/bin/workflow-engine"]
