ARG GO_IMAGE=golang:1.24.7-alpine3.22@sha256:fc2cff6625f3c1c92e6c85938ac5bd09034ad0d4bc2dfb08278020b68540dbb5
ARG RUNTIME_IMAGE=alpine:3.22@sha256:49340b346798875b5592e5d420241f2f9cfba5f47e62ebb1446a1b34734e898f

FROM ${GO_IMAGE} AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/billmesh ./cmd/billmesh

FROM builder AS tester
RUN apk add --no-cache build-base
ENV CGO_ENABLED=1

FROM ${RUNTIME_IMAGE} AS runtime
ARG APP_VERSION=unknown
ARG APP_REVISION=unknown
ARG APP_CREATED=unknown
ARG APP_SOURCE=unknown
LABEL org.opencontainers.image.title="Billmesh API" \
      org.opencontainers.image.version="${APP_VERSION}" \
      org.opencontainers.image.revision="${APP_REVISION}" \
      org.opencontainers.image.created="${APP_CREATED}" \
      org.opencontainers.image.source="${APP_SOURCE}"
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 billmesh \
    && adduser -S -D -H -u 10001 -G billmesh billmesh
WORKDIR /app
COPY --from=builder /out/billmesh /app/billmesh
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/billmesh"]
