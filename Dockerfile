# Build
FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /azul ./cmd/azul

# Run: a static binary on an image with no shell, as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /azul /azul
EXPOSE 8080
# Add -ip-header for your proxy (see docs/DEPLOY.md) so rate limits are per visitor.
ENTRYPOINT ["/azul", "serve", "-public", "-addr", "0.0.0.0:8080"]
