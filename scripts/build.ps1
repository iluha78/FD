# Refresh PATH from registry to pick up new installs
$env:Path = [System.Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [System.Environment]::GetEnvironmentVariable('Path','User')

Write-Host "=== Checking tools ===" -ForegroundColor Cyan
Write-Host "Go: $(go version 2>&1)"
Write-Host "GCC: $(gcc --version 2>&1 | Select-Object -First 1)"

Write-Host ""
Set-Location D:\Projects\FD
go mod tidy

Write-Host ""
Write-Host "=== Building API ===" -ForegroundColor Cyan
$env:CGO_ENABLED = "1"
go build -o bin/api.exe ./cmd/api
if ($LASTEXITCODE -eq 0) { Write-Host "  OK" -ForegroundColor Green } else { Write-Host "  FAILED" -ForegroundColor Red }

Write-Host "=== Building Ingestor ===" -ForegroundColor Cyan
go build -o bin/ingestor.exe ./cmd/ingestor
if ($LASTEXITCODE -eq 0) { Write-Host "  OK" -ForegroundColor Green } else { Write-Host "  FAILED" -ForegroundColor Red }

Write-Host "=== Building Worker ===" -ForegroundColor Cyan
go build -o bin/worker.exe ./cmd/worker
if ($LASTEXITCODE -eq 0) { Write-Host "  OK" -ForegroundColor Green } else { Write-Host "  FAILED" -ForegroundColor Red }

Write-Host ""
Write-Host "=== Done ===" -ForegroundColor Cyan
Get-ChildItem bin/*.exe | ForEach-Object { Write-Host "  $($_.Name) ($([math]::Round($_.Length/1MB, 1)) MB)" }
