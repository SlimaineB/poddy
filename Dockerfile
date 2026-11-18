FROM golang:1.26.11@sha256:4165ee6bf3b7811fd7b40373290405c83d0167dcb87ee5f29c427cdcef45ff8f AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /poddy . \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /poddy-bastion ./cmd/poddy-bastion \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /poddy-agent ./cmd/poddy-agent

FROM gcr.io/distroless/static:nonroot
COPY --from=build /poddy /poddy
COPY --from=build /poddy-bastion /poddy-bastion
COPY --from=build /poddy-agent /usr/local/bin/poddy-agent
EXPOSE 8080 2222
ENTRYPOINT ["/poddy"]
