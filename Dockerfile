# One distroless container carries the binary, the built app, and the
# media tools. The runtime stage has no shell and no package manager, so the
# Go build runs in a builder stage and every other artifact arrives as a
# copied file. Paths stay absolute and the entrypoint is absolute, because
# the runtime stage has no working directory semantics to rely on.
#
# Media tools arrive pinned. The pair below is the same static build CI
# installs, so renders measure with the same binaries everywhere. The render
# passes absolute paths to these files, because the runtime stage sets no
# PATH to look them up on.
ARG VERSION=dev
FROM golang:1.27.1 AS build
ARG VERSION
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$VERSION" -o /out/reprise ./cmd/reprise

FROM node:26 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

# The media pair is pinned by digest, and it is the build the render library
# measures its own fixtures against. Loudness and peak numbers only compare
# when the tool matches, so the gate and this image name one digest between
# them. The binaries are static and run on the distroless stage with no libs.
FROM mwader/static-ffmpeg:9.0.1@sha256:54e55b0cb8f672870fc38ceb2e6c411855cb3b39c505f5f3b2505ee01ed5f2b7 AS mediatools

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /srv
COPY --from=build /out/reprise /srv/reprise
COPY --from=web /web/build /srv/web/build
COPY --from=mediatools /ffmpeg /usr/local/bin/ffmpeg
COPY --from=mediatools /ffprobe /usr/local/bin/ffprobe
COPY config/reprise.box.toml /srv/config/reprise.box.toml
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/srv/reprise", "-web", "/srv/web/build"]
