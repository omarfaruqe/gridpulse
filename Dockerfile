# One image holds every binary; docker-compose.yml picks which one each service runs.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN mkdir /out && go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/...

# distroless/static carries CA certificates and time zone data, which the
# extractors need to reach the data providers over HTTPS.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ /app/
USER nonroot:nonroot
EXPOSE 8080
CMD ["/app/api", "-addr", ":8080"]
