FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /skael-server ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=build /skael-server /skael-server
EXPOSE 8080
ENTRYPOINT ["/skael-server"]
