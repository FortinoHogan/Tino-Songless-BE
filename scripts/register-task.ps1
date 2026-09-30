# Registers a weekly Windows scheduled task (Sunday 03:00). Run once in PowerShell.
$script = Join-Path $PSScriptRoot "import-songs.ps1"
$action = New-ScheduledTaskAction -Execute "powershell.exe" -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$script`""
$trigger = New-ScheduledTaskTrigger -Weekly -DaysOfWeek Sunday -At 3am
Register-ScheduledTask -TaskName "SongBattleImport" -Action $action -Trigger $trigger -Force
Write-Host "Registered. Test now with: Start-ScheduledTask -TaskName SongBattleImport"
