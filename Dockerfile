# syntax=docker/dockerfile:1

FROM gcr.io/distroless/static-debian12:nonroot AS runtime-base

FROM golang:1.26-bookworm AS app-build
WORKDIR /home/app/src

COPY --from=runtime-base /etc/passwd /home/app/rootfs/etc/passwd
COPY --from=runtime-base /etc/group /home/app/rootfs/etc/group

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    mkdir -p \
        /home/app/out \
        /home/app/rootfs/home/app/.config/agentSDK/data \
        /home/app/rootfs/home/app/.config/agentSDK/logs \
    && printf 'app:x:10001:10001:app:/home/app:/sbin/nologin\n' >> /home/app/rootfs/etc/passwd \
    && printf 'app:x:10001:\n' >> /home/app/rootfs/etc/group \
    && printf '{}\n' > /home/app/rootfs/home/app/.config/agentSDK/settings.json \
    && ln -s /home/app/out/proxy /home/app/rootfs/home/app/app \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /home/app/out/proxy .

FROM runtime-base
WORKDIR /home/app

COPY --from=app-build /home/app/rootfs/etc/passwd /etc/passwd
COPY --from=app-build /home/app/rootfs/etc/group /etc/group
COPY --from=app-build --chown=10001:10001 /home/app/out ./out
COPY --from=app-build --chown=10001:10001 /home/app/rootfs/home/app/ ./

EXPOSE 8317

USER app

ENTRYPOINT ["/home/app/app"]
CMD ["--port", "8317"]
