ARG DOCKER_REGISTRY=docker.io
FROM ${DOCKER_REGISTRY}/golang:1.27.3 AS build
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
COPY . /workspace
WORKDIR /workspace
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=vendor -ldflags "-s" -a -installsuffix cgo -o /main ./cmd/moot-sweep

FROM ${DOCKER_REGISTRY}/alpine:3.23
ARG BUILD_GIT_COMMIT=none
ARG BUILD_DATE=unknown
# gh is required: the sweep reads PR state through the gh CLI, the same path
# the reviewer uses. git backs gh's repository resolution.
RUN apk --no-cache add ca-certificates github-cli git
COPY --from=build /main /main
ENV ZONEINFO=/zoneinfo.zip
COPY --from=build /usr/local/go/lib/time/zoneinfo.zip /
ENV BUILD_GIT_COMMIT=${BUILD_GIT_COMMIT}
ENV BUILD_DATE=${BUILD_DATE}
ENTRYPOINT ["/main", "-v=2"]
