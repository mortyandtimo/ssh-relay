FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY apps ./apps
COPY packages ./packages
RUN go build -o /out/relay-tcp ./apps/relay-tcp/cmd/relay-tcp

FROM alpine:3.20
COPY --from=build /out/relay-tcp /usr/local/bin/relay-tcp
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/relay-tcp"]
