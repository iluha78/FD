$env:Path = [System.Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [System.Environment]::GetEnvironmentVariable('Path','User')
$env:CGO_ENABLED = "1"
Set-Location D:\Projects\FD
Write-Host "Go: $(go version)"
Write-Host "GCC: $(gcc --version | Select-Object -First 1)"
Write-Host "ORT Go wrapper: $((Select-String 'onnxruntime_go' go.mod | Select-Object -First 1).Line.Trim())"
Write-Host ""
Write-Host "=== Building Worker ===" -ForegroundColor Cyan
go build -o bin/worker.exe ./cmd/worker
if ($LASTEXITCODE -eq 0) {
    Write-Host "  Worker OK" -ForegroundColor Green
} else {
    Write-Host "  Worker FAILED" -ForegroundColor Red
}
