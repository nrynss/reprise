# One distroless container carries the binary and the built app.
# The container has no shell and no package manager, so the Go build runs in a
# builder stage and the runtime stage copies artifacts only. Paths stay
# absolute and the entrypoint is absolute, because the runtime stage has no
# working directory semantics to rely on.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
RUN CGO_ENABLED=0 go build -trimpath -o /out/reprise ./cmd/reprise

FROM node:26 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /srv
COPY --from=build /out/reprise /srv/reprise
COPY --from=web /web/build /srv/web/build
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/srv/reprise", "-web", "/srv/web/build"]
