# syntax=docker/dockerfile:1

FROM docker.io/library/golang:1.26.11@sha256:4165ee6bf3b7811fd7b40373290405c83d0167dcb87ee5f29c427cdcef45ff8f AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy . \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy-bastion ./cmd/poddy-bastion \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy-agent ./cmd/poddy-agent

FROM gcr.io/distroless/static:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build --chmod=0755 /out/poddy /poddy
COPY --from=build --chmod=0755 /out/poddy-bastion /poddy-bastion
COPY --from=build --chmod=0755 /out/poddy-agent /usr/local/bin/poddy-agent
EXPOSE 8080 2222
ENTRYPOINT ["/poddy"]
