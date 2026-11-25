param([ValidateSet("run","import","reindex","validate","doctor","whoami","stats","help")][string]$Command = "run")
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
if (-not (Test-Path ".env")) {
    Copy-Item ".env.example" ".env"
    Write-Host "Created .env. Fill TELEGRAM_BOT_TOKEN and TELEGRAM_OWNER_ID, then run again."
    exit 1
}
if (-not (Test-Path "bin\iu-bot.exe")) {
    New-Item -ItemType Directory -Path "bin" -Force | Out-Null
    go build -trimpath -o "bin\iu-bot.exe" ./cmd/iu-bot
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
& ".\bin\iu-bot.exe" $Command
exit $LASTEXITCODE
