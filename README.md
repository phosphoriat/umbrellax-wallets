# Project Name

UmbrellaX Wallets microservice

## Tech Stack

- [Go](https://golang.org/) 1.25+
- [Fiber](https://gofiber.io/) v2
- [PostgreSQL](https://www.postgresql.org/) v18

## Getting Started

### Prerequisites

- Go 1.25 or higher
- PostgreSQL

### Installation
```bash
git clone https://github.com/username/project.git
cd project
go mod download
```

### Running
```bash
# Development
go run cmd/main.go

# With Air (hot reload)
air

# Production build
go build -o bin/app cmd/main.go
./bin/app
```