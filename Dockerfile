FROM golang:1.26.6 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN mkdir -p /profile-images && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /shipping-service ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /shipping-service /shipping-service
COPY --from=build /src/web /web
COPY --from=build --chown=nonroot:nonroot /profile-images /app/data/profile-images
WORKDIR /
EXPOSE 8090
USER nonroot:nonroot
ENTRYPOINT ["/shipping-service"]
