FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS cli
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/sota ./cmd/sota

FROM --platform=$BUILDPLATFORM python:3.13-alpine AS core
ARG TARGETARCH
WORKDIR /verify
COPY core.lock.json ./
COPY vendor/kittend /cores
RUN python3 -c 'import hashlib,json,os,pathlib,shutil; arch=os.environ["TARGETARCH"]; source=pathlib.Path("/cores")/arch/"kittend"; expected=json.load(open("core.lock.json"))[arch]["core_sha256"]; assert hashlib.sha256(source.read_bytes()).hexdigest()==expected,"Core checksum mismatch"; shutil.copyfile(source,"/kittend"); os.chmod("/kittend",0o755)'

FROM alpine:3.22 AS runtime-base

FROM --platform=$BUILDPLATFORM alpine:3.22 AS runtime
ARG TARGETARCH
COPY --from=runtime-base / /rootfs/
# Extract target packages with the native package manager; never execute target scripts.
RUN case "$TARGETARCH" in amd64) apk_arch=x86_64 ;; arm64) apk_arch=aarch64 ;; *) exit 1 ;; esac \
    && mkdir -p /rootfs/etc/apk/keys \
    && cp /usr/share/apk/keys/"$apk_arch"/* /rootfs/etc/apk/keys/ \
    && cp /etc/apk/repositories /rootfs/etc/apk/ \
    && apk --arch "$apk_arch" --root /rootfs --no-scripts add --no-cache ca-certificates-bundle iproute2 \
    && mkdir -p /rootfs/run/sota-cli /rootfs/var/lib/sota-cli \
    && chmod 700 /rootfs/run/sota-cli /rootfs/var/lib/sota-cli

FROM runtime-base
COPY --from=runtime /rootfs/ /
COPY --from=cli /out/sota /usr/local/bin/sota
COPY --from=core /kittend /usr/local/bin/kittend
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/sota"]
CMD ["help"]
