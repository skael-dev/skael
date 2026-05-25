# Stage 1: Generate OpenAPI spec
FROM golang:1.24 AS spec
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p web/dist && touch web/dist/placeholder
RUN go run ./cmd/server --openapi > /openapi.json

# Stage 2: Build React SPA
FROM node:22-slim AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
COPY --from=spec /openapi.json ./openapi.json
RUN npm run generate && npm run build

# Stage 3: Build Go binary with embedded SPA
FROM golang:1.24 AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -o /skael-server ./cmd/server

# Stage 4: Minimal runtime
FROM gcr.io/distroless/static-debian12
COPY --from=go /skael-server /skael-server
EXPOSE 8080
ENTRYPOINT ["/skael-server"]
