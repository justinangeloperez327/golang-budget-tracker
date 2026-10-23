FROM node:24-alpine AS assets
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm install --ignore-scripts --no-audit --no-fund
COPY scripts ./scripts
COPY web ./web
COPY internal/views ./internal/views
RUN npm run assets:build

FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=assets /src/internal/assets/app.css ./internal/assets/app.css
COPY --from=assets /src/internal/assets/app.js ./internal/assets/app.js
COPY --from=assets /src/internal/assets/htmx.min.js ./internal/assets/htmx.min.js
RUN go run github.com/a-h/templ/cmd/templ@v0.3.1020 generate && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/budget-tracker ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/budget-tracker /usr/local/bin/budget-tracker
USER 10001:10001
EXPOSE 8080
ENV APP_ENV=production HTTP_ADDR=:8080
ENTRYPOINT ["/usr/local/bin/budget-tracker"]
