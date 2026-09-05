FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /shipping-service ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /shipping-service /shipping-service
COPY --from=build /src/web /web
WORKDIR /
EXPOSE 8090
USER nonroot:nonroot
ENTRYPOINT ["/shipping-service"]
