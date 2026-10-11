ARG DOCKER_REGISTRY=docker.io
FROM ${DOCKER_REGISTRY}/golang:1.27.2 AS build
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
COPY . /workspace
WORKDIR /workspace
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=vendor -ldflags "-s" -a -installsuffix cgo -o /main
CMD ["/bin/bash"]

FROM ${DOCKER_REGISTRY}/alpine:3.23 AS alpine
RUN apk --no-cache add ca-certificates curl bash nodejs npm github-cli git ast-grep python3 jq \
 && npm install -g --omit=dev --no-optional @anthropic-ai/claude-code \
 && npm cache clean --force \
 && apk del npm \
 && rm -rf /root/.npm /tmp/*

FROM alpine
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
COPY --from=build /main /main
COPY agent/ /agent/
ENV HOME=/home/claude
ENV CLAUDE_CONFIG_DIR=/home/claude/.claude
# Deny WebSearch/WebFetch for every session in this image (see
# claude-config/settings.json). A vLLM-backed backend — the reviewer routes via
# claude-code-router to seibert-vllm-external — rejects the `server_tool_use`
# content block the CLI emits when WebSearch runs, with
# `400 Content-Block-Typ "server_tool_use" wird nicht unterstützt`. That kills
# the run mid-flight, and the resulting no-verdict review body used to post as
# CHANGES_REQUESTED (bborbe/nuke#446).
#
# `permissions.deny` is the lever, NOT `--allowedTools`: the allowlist is
# permission-only and never removes a tool from the toolset, so the reviewer's
# existing `executionTools` list never excluded WebSearch in the first place.
# `permissions.deny` removes both tools from the toolset outright, so the model
# cannot call WebSearch and the CLI never issues the side-query that carries the
# `web_search_20250305` server tool. (Verified against claude-code 2.1.296 by
# reading the `tools` array of the `system`/init event.)
COPY claude-config/settings.json /home/claude/.claude/settings.json
RUN set -eux \
 && mkdir -p /home/claude/.claude \
 && timeout 300 claude plugin marketplace add bborbe/coding@v0.51.0 \
 && timeout 300 claude plugin install coding \
 && claude plugin list | grep -q coding
ENV ZONEINFO=/zoneinfo.zip
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /
ENV BUILD_GIT_COMMIT=${BUILD_GIT_COMMIT}
ENV BUILD_DATE=${BUILD_DATE}
ENTRYPOINT ["/main", "-v=2"]
