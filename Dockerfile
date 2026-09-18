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

FROM scratch AS mediatools
# Pinned static media binaries from the same author CI downloads. The binary
# release archive CI names is gone upstream, so this stage pulls the same
# author's pinned 7.1 image instead and copies the pair out of it. The
# binaries are static, so they run on the distroless stage with no libs.
COPY --from=mwader/static-ffmpeg:7.1 /ffmpeg /ffmpeg
COPY --from=mwader/static-ffmpeg:7.1 /ffprobe /ffprobe

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
