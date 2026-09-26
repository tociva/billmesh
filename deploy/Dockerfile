FROM golang:1.24.7-alpine3.22 AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/billmesh ./cmd/billmesh

FROM builder AS tester
RUN apk add --no-cache build-base
ENV CGO_ENABLED=1

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates && addgroup -S billmesh && adduser -S -G billmesh billmesh
COPY --from=builder /out/billmesh /app/billmesh
USER billmesh
ENTRYPOINT ["/app/billmesh"]
