$ErrorActionPreference = "Stop"

function Invoke-NativeStep {
    param(
        [Parameter(Mandatory=$true)][string]$Name,
        [Parameter(Mandatory=$true)][scriptblock]$Command
    )

    Write-Host ""
    Write-Host "=== $Name ===" -ForegroundColor Cyan

    & $Command

    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE"
    }
}

$previousParallelLimit = $env:COMPOSE_PARALLEL_LIMIT
$env:COMPOSE_PARALLEL_LIMIT = "1"

try {
    Invoke-NativeStep "Go tests" {
        go test ./...
    }

    Invoke-NativeStep "Go vet" {
        go vet ./...
    }

    Invoke-NativeStep "Compose validation" {
        docker compose config --quiet
    }

    Invoke-NativeStep "Start complete stack" {
        docker compose up -d --build
    }

    Invoke-NativeStep "P1 API integration" {
        docker compose --profile test run --build --rm p1-api-integration-test
    }

    Invoke-NativeStep "Database privilege boundaries" {
        docker compose --profile test run --rm db-security-test
    }

    Invoke-NativeStep "Audit immutability and idempotency" {
        docker compose --profile test run --rm audit-invariants-test
    }

    Invoke-NativeStep "Audit relay delivery" {
        docker compose --profile test run --rm audit-relay-test
    }

    Write-Host ""
    Write-Host "All P1 verification checks passed." -ForegroundColor Green
}
finally {
    if ($null -eq $previousParallelLimit) {
        Remove-Item Env:COMPOSE_PARALLEL_LIMIT -ErrorAction SilentlyContinue
    }
    else {
        $env:COMPOSE_PARALLEL_LIMIT = $previousParallelLimit
    }
}
