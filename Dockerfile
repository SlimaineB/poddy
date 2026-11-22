# syntax=docker/dockerfile:1

FROM docker.io/library/golang:1.27.4 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy . \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy-bastion ./cmd/poddy-bastion \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy-agent ./cmd/poddy-agent \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy-mesh-bootstrap ./cmd/poddy-mesh-bootstrap

FROM gcr.io/distroless/static:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build --chmod=0755 /out/poddy /poddy
COPY --from=build --chmod=0755 /out/poddy-bastion /poddy-bastion
COPY --from=build --chmod=0755 /out/poddy-agent /usr/local/bin/poddy-agent
COPY --from=build --chmod=0755 /out/poddy-mesh-bootstrap /poddy-mesh-bootstrap
EXPOSE 8080 2222
ENTRYPOINT ["/poddy"]
