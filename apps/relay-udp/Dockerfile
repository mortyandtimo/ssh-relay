FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/relay-udp ./apps/relay-udp/cmd/relay-udp

FROM gcr.io/distroless/base-debian12
COPY --from=build /out/relay-udp /usr/local/bin/relay-udp
EXPOSE 9093/udp 9093
ENTRYPOINT ["/usr/local/bin/relay-udp"]
