FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY agents/ agents/
COPY mock/ mock/
# KW BuildKit is the CI: a failing vet or test stops the image from publishing.
RUN CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 go test ./... -timeout 180s
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server && CGO_ENABLED=0 go build -trimpath -o /out/mock-backend ./cmd/mock-backend

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 demo
WORKDIR /app
COPY --from=backend /out/ /app/bin/
COPY --from=web /src/web/dist/ /app/web/dist/
COPY agents/ /app/agents/
COPY mock/ /app/mock/
USER demo
ENV LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
CMD ["/app/bin/server"]
