
param (
    [string]$command
)

switch ($command) {
    "run-backend" {
        Write-Host "Starting Backend..."
        cd backend
        go run ./cmd/server
    }
    "run-frontend" {
        Write-Host "Starting Frontend..."
        cd frontend
        npm run dev
    }
    "docker-up" {
        Write-Host "Starting Docker Compose..."
        docker compose up --build
    }
    "docker-down" {
        Write-Host "Stopping Docker Compose..."
        docker compose down
    }
    Default {
        Write-Host "Usage: .\manage.ps1 [command]"
        Write-Host "Commands:"
        Write-Host "  run-backend   - Start the Go backend"
        Write-Host "  run-frontend  - Start the React frontend"
        Write-Host "  docker-up     - Start services with Docker Compose"
        Write-Host "  docker-down   - Stop Docker Compose services"
    }
}
