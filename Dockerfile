# syntax=docker/dockerfile:1

# Build stage. Defaults to the latest Go-on-Alpine image; pass GO_IMAGE to pin.
ARG GO_IMAGE=golang:alpine
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go version && CGO_ENABLED=0 GOOS=linux go build -trimpath -o /bin/slack-utils .

# Runtime stage
FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /bin/slack-utils /app/slack-utils
ENTRYPOINT ["/app/slack-utils"]
CMD ["-h"]
