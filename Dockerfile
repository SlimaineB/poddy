FROM golang:1.27.3@sha256:e519d3b0a5b285e5aa53ab9eef02bc6bbb9cb04608389b743a136a9ce75f0d1e AS build
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
