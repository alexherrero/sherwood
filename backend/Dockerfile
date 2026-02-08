FROM golang:1.23-alpine

WORKDIR /app

# Install git for fetch dependencies if needed
RUN apk add --no-cache git

COPY go.mod ./
# COPY go.sum ./ 
# go.sum might not exist yet if no deps added

RUN go mod download

COPY . .

# Expose port
EXPOSE 8080

# Run the application
CMD ["go", "run", "./cmd/server"]
