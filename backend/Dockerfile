FROM golang:1.23-alpine AS build

WORKDIR /app
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -o /ticket-api ./cmd/api

FROM alpine:3.21
WORKDIR /app
COPY --from=build /ticket-api /ticket-api
EXPOSE 8080
CMD ["/ticket-api"]
