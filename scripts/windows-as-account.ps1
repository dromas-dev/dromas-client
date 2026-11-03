# scripts/windows-as-account.ps1 <account> <mode>: scripts/check.sh <mode>, in Git's
# bash, in a process of the account (plain, other, system, network-service,
# local-service, guest), by a scheduled task, which starts a process of any of them
# from a job. Its output comes back here and its status is this script's.
# scripts/windows-accounts.ps1 makes the accounts.
param([string]$Account, [string]$Mode)
$ErrorActionPreference = 'Stop'

$root = 'C:\dromas-accounts'
$task = "dromas-$Account"
$out, $done, $cmd = "$root\out\$task.txt", "$root\out\$task.done", "$root\out\$task.cmd"

# The account's own environment, its TEMP and its profile, with what the job's tools
# and tests need beside them.
$lines = @('@echo off')
foreach ($v in Get-ChildItem Env: | Where-Object { $_.Name -match '^(PATH|GO.*|DROMAS_TEST_.*)$' }) {
  $lines += "set `"$($v.Name)=$($v.Value)`""
}
$lines += "set `"DROMAS_TEST_ACCOUNT=$Account`""
$lines += "cd /d `"$env:GITHUB_WORKSPACE`""
$lines += "`"C:\Program Files\Git\bin\bash.exe`" scripts/check.sh $Mode > `"$out`" 2>&1"
$lines += "echo status=%errorlevel%> `"$done`""
$lines | Set-Content -Encoding ascii -Path $cmd

$action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument "/c `"$cmd`""
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit 0
$service = @{ 'system' = 'NT AUTHORITY\SYSTEM'; 'network-service' = 'NT AUTHORITY\NETWORK SERVICE'; 'local-service' = 'NT AUTHORITY\LOCAL SERVICE' }[$Account]
if ($service) {
  $principal = New-ScheduledTaskPrincipal -UserId $service -LogonType ServiceAccount
  Register-ScheduledTask -TaskName $task -Action $action -Settings $settings -Principal $principal | Out-Null
} else {
  $user = if ($Account -eq 'guest') { (Get-LocalUser | Where-Object { $_.SID.Value -like '*-501' }).Name } else { "dromas-$Account" }
  Register-ScheduledTask -TaskName $task -Action $action -Settings $settings -User $user -Password $env:DROMAS_ACCOUNTS_PASSWORD -RunLevel Limited | Out-Null
}
New-Item -ItemType File -Force -Path $out | Out-Null
Start-ScheduledTask -TaskName $task

# What the task has written so far, as it writes it.
$reader = [IO.FileStream]::new($out, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete)
$stdout = [Console]::OpenStandardOutput()
$status = 1
$start = Get-Date
while ($true) {
  $finished = Test-Path $done
  $reader.CopyTo($stdout)
  $stdout.Flush()
  if ($finished) {
    $status = [int]((Get-Content $done) -replace '[^0-9]', '')
    break
  }
  # 0x41303: not yet run, which a logon refused leaves too. 0x41301: running.
  $result = (Get-ScheduledTaskInfo -TaskName $task).LastTaskResult
  $elapsed = (Get-Date) - $start
  if ($result -eq 0x41303 -and $elapsed.TotalMinutes -gt 2) {
    Write-Output "::error::the task as $Account never started"
    break
  }
  if ($elapsed.TotalMinutes -gt 30) {
    Write-Output "::error::as $Account, scripts/check.sh $Mode did not finish"
    break
  }
  Start-Sleep -Seconds 5
}
$reader.Close()
Unregister-ScheduledTask -TaskName $task -Confirm:$false
exit $status
