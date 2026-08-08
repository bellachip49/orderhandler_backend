FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /out/orders_backend .

FROM alpine:latest
RUN adduser -D -H orders_backend && mkdir /data && chown orders_backend /data
COPY --from=build /out/orders_backend /orders_backend
USER orders_backend
ENV HOST=0.0.0.0
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/orders_backend"]
