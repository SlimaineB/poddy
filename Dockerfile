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
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy-mesh-bootstrap ./cmd/poddy-mesh-bootstrap \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/poddy-netmon ./cmd/poddy-netmon

FROM gcr.io/distroless/static:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
COPY --from=build --chmod=0755 /out/poddy /poddy
COPY --from=build --chmod=0755 /out/poddy-bastion /poddy-bastion
COPY --from=build --chmod=0755 /out/poddy-agent /usr/local/bin/poddy-agent
COPY --from=build --chmod=0755 /out/poddy-mesh-bootstrap /poddy-mesh-bootstrap
COPY --from=build --chmod=0755 /out/poddy-netmon /poddy-netmon
EXPOSE 8080 2222 9102
ENTRYPOINT ["/poddy"]
