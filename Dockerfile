FROM node:22-alpine AS ui
WORKDIR /ui
COPY ui/package.json ui/package-lock.json* ./
RUN npm install
COPY ui/ ./
RUN npm run build

FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
COPY --from=ui /ui/dist ./ui/dist
RUN CGO_ENABLED=0 go build -o /scrapy ./cmd/scrapy

FROM gcr.io/distroless/static-debian12
COPY --from=build /scrapy /scrapy
EXPOSE 8080
ENTRYPOINT ["/scrapy"]
