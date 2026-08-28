# Build the server as a static binary and ship it on a base image with no shell,
# no package manager and nothing else to attack (docs/08 §9).

FROM golang:1.25-alpine AS build
WORKDIR /src

# Dependencies first: they change far less often than the code, so this layer
# stays cached across almost every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/sente-server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sente-server /sente-server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/sente-server"]
