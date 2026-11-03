# scripts/windows-accounts.ps1: what CI's windows-accounts job runs its tests with,
# on a runner it may change at will (never a machine you keep, which it would leave
# with two new users, two new volumes and a wider logon right). Needs
# an elevated shell.
#
# Two plain users, dromas-plain and dromas-other, members of Users alone.
# A folder every account can use, with the Go caches in it. A FAT32 and an exFAT
# volume, each a virtual disk (diskpart), which keep no access list.
$ErrorActionPreference = 'Stop'

$root = 'C:\dromas-accounts'
New-Item -ItemType Directory -Force -Path "$root\out", "$root\mod", "$root\gocache", "$root\reach" | Out-Null
foreach ($dir in $root, $env:GITHUB_WORKSPACE) {
  # Everyone, by its SID: the folder to write, the checkout to read.
  $right = if ($dir -eq $root) { 'M' } else { 'RX' }
  icacls $dir /grant "*S-1-1-0:(OI)(CI)$right" /T /Q
  if ($LASTEXITCODE -ne 0) { throw "icacls $dir" }
}

# A throwaway account's password, on a runner that lives for this job.
$password = 'Aa1!' + [guid]::NewGuid().ToString('N')
Write-Output "::add-mask::$password"
Add-Content -Path $env:GITHUB_ENV -Value "DROMAS_ACCOUNTS_PASSWORD=$password"
$secure = ConvertTo-SecureString $password -AsPlainText -Force
foreach ($name in 'dromas-plain', 'dromas-other') {
  New-LocalUser -Name $name -Password $secure -PasswordNeverExpires | Out-Null
  Add-LocalGroupMember -SID 'S-1-5-32-545' -Member $name
}

# A scheduled task needs its account to hold the right to log on as a batch job,
# which on this image only the Administrators and service groups do; without it the
# task never starts and says nothing. Everyone holds it here (secedit, the
# documented way to change a local security policy).
secedit /export /cfg "$root\rights.inf" /areas USER_RIGHTS | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'secedit /export' }
(Get-Content "$root\rights.inf") -replace '^SeBatchLogonRight = (.*)$', 'SeBatchLogonRight = $1,*S-1-1-0' | Set-Content -Encoding Unicode "$root\rights.inf"
secedit /configure /db "$root\rights.sdb" /cfg "$root\rights.inf" /areas USER_RIGHTS | Out-Null
if ($LASTEXITCODE -ne 0) { throw 'secedit /configure' }

$volumes = @()
foreach ($volume in @(@{ Fs = 'fat32'; Letter = 'X' }, @{ Fs = 'exfat'; Letter = 'Y' })) {
  $script = "$root\$($volume.Fs).diskpart"
  @"
create vdisk file="$root\$($volume.Fs).vhdx" maximum=128 type=expandable
select vdisk file="$root\$($volume.Fs).vhdx"
attach vdisk
create partition primary
format fs=$($volume.Fs) quick
assign letter=$($volume.Letter)
"@ | Set-Content -Encoding ascii -Path $script
  diskpart /s $script
  if ($LASTEXITCODE -ne 0) { throw "diskpart $($volume.Fs)" }
  $volumes += "$($volume.Letter):\"
}
Add-Content -Path $env:GITHUB_ENV -Value "DROMAS_TEST_NO_ACL_VOLUMES=$($volumes -join ';')"
Add-Content -Path $env:GITHUB_ENV -Value "DROMAS_TEST_REACH=$root\reach"
