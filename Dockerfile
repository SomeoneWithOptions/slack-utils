# syntax=docker/dockerfile:1

# Build stage
FROM golang:alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/slack-utils ./...

# Runtime stage
FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /bin/slack-utils /app/slack-utils
ENTRYPOINT ["/app/slack-utils"]
CMD ["-h"]
