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

FROM alpine:3.22
RUN apk add --no-cache ca-certificates iproute2 && mkdir -p /run/sota-cli /var/lib/sota-cli && chmod 700 /run/sota-cli /var/lib/sota-cli
COPY --from=cli /out/sota /usr/local/bin/sota
COPY --from=core /kittend /usr/local/bin/kittend
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/sota"]
CMD ["help"]
