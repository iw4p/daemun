# Static build — no cgo, no libc, so the final image needs nothing but the binary.
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/webhook .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/webhook /webhook
EXPOSE 8443
USER nonroot:nonroot
ENTRYPOINT ["/webhook"]
