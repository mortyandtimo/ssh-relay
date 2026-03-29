FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY apps ./apps
COPY packages ./packages
RUN go build -o /out/relay-https ./apps/relay-https/cmd/relay-https

FROM alpine:3.20
COPY --from=build /out/relay-https /usr/local/bin/relay-https
EXPOSE 9092
ENTRYPOINT ["/usr/local/bin/relay-https"]
