# syntax=docker/dockerfile:1

# Build stage
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/slack-export ./...

# Runtime stage
FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /bin/slack-export /app/slack-export
COPY --from=build /src/users.json /app/users.json
ENTRYPOINT ["/app/slack-export"]
CMD ["-h"]
