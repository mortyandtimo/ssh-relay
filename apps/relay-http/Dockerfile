FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY apps ./apps
COPY packages ./packages
RUN go build -o /out/relay-http ./apps/relay-http/cmd/relay-http

FROM alpine:3.20
COPY --from=build /out/relay-http /usr/local/bin/relay-http
EXPOSE 9091
ENTRYPOINT ["/usr/local/bin/relay-http"]
