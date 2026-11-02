#!/bin/sh
# Starts the Workflow Runtime from conf/runtime.toml. Other workflow-engine commands pass through:
#   workflow-runtime.sh [start] [--allow-no-auth] | render-config | version
# WORKFLOW_RUNTIME_CONFIG names another runtime.toml. Relative paths in it resolve against the runtime's home.
set -eu
home=$(cd "$(dirname "$0")/.." && pwd -P)
engine="$home/bin/workflow-engine"
config="${WORKFLOW_RUNTIME_CONFIG:-$home/conf/runtime.toml}"
cd "$home"
command="${1:-start}"
[ $# -gt 0 ] && shift
case "$command" in
  start | render-config) exec "$engine" "$command" --runtime-config "$config" "$@" ;;
  *) exec "$engine" "$command" "$@" ;;
esac
