FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/budget-tracker ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/budget-tracker /usr/local/bin/budget-tracker
USER 10001:10001
EXPOSE 8080
ENV APP_ENV=production HTTP_ADDR=:8080
ENTRYPOINT ["/usr/local/bin/budget-tracker"]
