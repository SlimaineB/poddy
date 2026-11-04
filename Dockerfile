FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /poddy . \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /poddy-bastion ./cmd/poddy-bastion

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /poddy /poddy
COPY --from=build /poddy-bastion /poddy-bastion
EXPOSE 8080 2222
ENTRYPOINT ["/poddy"]
