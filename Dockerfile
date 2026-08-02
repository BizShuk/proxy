# syntax=docker/dockerfile:1

FROM golang:1.26-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN mkdir -p /out/config-dir \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxy .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=builder /out/proxy /usr/local/bin/proxy
COPY --from=builder --chown=65532:65532 /out/config-dir /home/nonroot/.config/agentSDK

EXPOSE 8317

ENTRYPOINT ["/usr/local/bin/proxy"]
CMD ["--port", "8317"]
