# syntax=docker/dockerfile:1

FROM gcr.io/distroless/static-debian12:nonroot AS runtime-base

FROM golang:1.26-bookworm AS builder
WORKDIR /src

COPY --from=runtime-base /etc/passwd /out/passwd
COPY --from=runtime-base /etc/group /out/group

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN printf 'app:x:10001:10001:app:/home/app:/sbin/nologin\n' >> /out/passwd \
    && printf 'app:x:10001:\n' >> /out/group \
    && mkdir -p /out/config-dir \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/proxy .

FROM runtime-base
WORKDIR /app

COPY --from=builder /out/passwd /etc/passwd
COPY --from=builder /out/group /etc/group
COPY --from=builder /out/proxy /usr/local/bin/proxy
COPY --from=builder --chown=10001:10001 /out/config-dir /home/app/.config/agentSDK

ENV HOME=/home/app

EXPOSE 8317

USER app

ENTRYPOINT ["/usr/local/bin/proxy"]
CMD ["--port", "8317"]
