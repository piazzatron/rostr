# Build a static binary, then run it on a small Alpine image (wget is there for the compose healthcheck).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /rostr-server .

FROM alpine:3.22
RUN adduser -D -H rostr
USER rostr
COPY --from=build /rostr-server /usr/local/bin/rostr-server
ENV PORT=8080
EXPOSE 8080
CMD ["rostr-server"]
