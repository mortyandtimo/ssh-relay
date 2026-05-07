FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/ssh-relay-api ./apps/ssh-relay-api/cmd/ssh-relay-api/

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /out/ssh-relay-api /usr/local/bin/ssh-relay-api
EXPOSE 7722
ENTRYPOINT ["ssh-relay-api"]
