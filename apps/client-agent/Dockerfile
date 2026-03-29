FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY apps ./apps
COPY packages ./packages
RUN go build -o /out/client-agent ./apps/client-agent/cmd/client-agent

FROM alpine:3.20
COPY --from=build /out/client-agent /usr/local/bin/client-agent
ENTRYPOINT ["/usr/local/bin/client-agent"]
