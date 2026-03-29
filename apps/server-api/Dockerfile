FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY apps ./apps
COPY packages ./packages
RUN go build -o /out/server-api ./apps/server-api/cmd/server-api

FROM alpine:3.20
COPY --from=build /out/server-api /usr/local/bin/server-api
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server-api"]
