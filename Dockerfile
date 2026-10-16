# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=${VERSION}" -o /out/blackbox ./cmd/blackbox

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/blackbox /blackbox
EXPOSE 8080
ENTRYPOINT ["/blackbox"]
CMD ["serve"]
