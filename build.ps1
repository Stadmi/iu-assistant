$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
go test ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
New-Item -ItemType Directory -Path "bin" -Force | Out-Null
go build -trimpath -o "bin\iu-bot.exe" ./cmd/iu-bot
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Built: bin\iu-bot.exe"
