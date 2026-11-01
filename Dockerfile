# SPDX-License-Identifier: AGPL-3.0-only
# Rewards: pure Go (CGO off), static binary, no shell, non-root. Migrations are embedded in
# the binary; reward assets live on the /data volume.
ARG GO_IMAGE=docker.io/library/golang:1.26-bookworm
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS TARGETARCH
ENV GOFLAGS=-buildvcs=false
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/rewards ./cmd/rewards
RUN mkdir -p /out/data/assets && chown -R 10001:10001 /out/data

FROM scratch
COPY --from=build /out/rewards /rewards
COPY --from=build --chown=10001:10001 /out/data /data
USER 10001:10001
ENTRYPOINT ["/rewards"]
