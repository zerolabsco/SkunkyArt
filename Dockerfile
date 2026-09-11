ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS build
ARG TARGETOS
ARG TARGETARCH
# Set by the release workflow from the git tag; --help and /api/instance report it.
ARG VERSION=dev

WORKDIR /build
COPY . .
RUN CGO_ENABLED=0 GOARCH="${TARGETARCH}" GOOS="${TARGETOS}" \
  go build -ldflags "-s -w -extldflags '-static' -X main.version=${VERSION}" && \
  echo "skunkyart:x:10000:10000:SkunkyArt user:/:/sbin/nologin" > /etc/minimal-passwd && \
  echo "skunkyart:x:10000:" > /etc/minimal-group

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /build/static /static
COPY --from=build /build/skunkyart /skunkyart
COPY --from=build /etc/minimal-passwd /etc/passwd
COPY --from=build /etc/minimal-group /etc/group

USER skunkyart

ENTRYPOINT ["/skunkyart"]
