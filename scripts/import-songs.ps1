# Runs the song importer from the backend folder (needed so .env is found) and appends output to logs\importer.log
$be = Split-Path -Parent $PSScriptRoot
Set-Location $be
New-Item -ItemType Directory -Force -Path logs | Out-Null
"=== $(Get-Date -Format s) ===" | Out-File -Append logs\importer.log
go run ./cmd/importer -sources sources.txt -limit 30 *>> logs\importer.log
