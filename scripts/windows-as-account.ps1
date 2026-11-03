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
$service = @{ 'system' = 'NT AUTHORITY\SYSTEM'; 'network-service' = 'NT AUTHORITY\NETWORK SERVICE'; 'local-service' = 'NT AUTHORITY\LOCAL SERVICE' }[$Account]
if ($service) {
  $principal = New-ScheduledTaskPrincipal -UserId $service -LogonType ServiceAccount
  Register-ScheduledTask -TaskName $task -Action $action -Principal $principal | Out-Null
} else {
  $user = if ($Account -eq 'guest') { (Get-LocalUser | Where-Object { $_.SID.Value -like '*-501' }).Name } else { "dromas-$Account" }
  Register-ScheduledTask -TaskName $task -Action $action -User $user -Password $env:DROMAS_ACCOUNTS_PASSWORD -RunLevel Limited | Out-Null
}
Start-ScheduledTask -TaskName $task

# 0x41303: not yet run. 0x41301: running. Any other result with no status written is
# a task that never ran its command, as a logon refused.
$status = 1
$deadline = (Get-Date).AddMinutes(30)
while ((Get-Date) -lt $deadline) {
  if (Test-Path $done) {
    $status = [int]((Get-Content $done) -replace '[^0-9]', '')
    break
  }
  $result = (Get-ScheduledTaskInfo -TaskName $task).LastTaskResult
  if ((Get-ScheduledTask -TaskName $task).State -eq 'Ready' -and $result -notin 0x41303, 0x41301) {
    if (-not (Test-Path $done)) {
      Write-Output ("::error::the task as {0} ended with 0x{1:x} and no status" -f $Account, $result)
      break
    }
  }
  Start-Sleep -Seconds 5
}
Unregister-ScheduledTask -TaskName $task -Confirm:$false
if (Test-Path $out) { [Console]::Out.Write([IO.File]::ReadAllText($out)) }
if (-not (Test-Path $done)) { Write-Output "::error::as $Account, scripts/check.sh $Mode did not finish" }
exit $status
