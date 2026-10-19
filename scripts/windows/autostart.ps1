param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('install', 'remove', 'status')]
    [string]$Action,

    [string]$Root = ''
)

$ErrorActionPreference = 'Stop'
if (-not $Root) {
    $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
    $Root = Split-Path -Parent (Split-Path -Parent $scriptDir)
}
$Root = (Resolve-Path -LiteralPath $Root).Path
$TaskName = 'hecate'
$RetryEvery = New-TimeSpan -Minutes 1
$RetryFor = New-TimeSpan -Days 365
$BootDelay = 'PT30S'

function Test-Administrator {
    $principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-HecateTask {
    return Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
}

function Install-HecateTask {
    if (-not (Test-Administrator)) {
        throw 'Run from an elevated PowerShell (Administrator): creating a scheduled task changes Windows.'
    }
    $exe = Join-Path $Root 'bin\hecate.exe'
    $config = Join-Path $Root 'hecate.yaml'
    foreach ($path in $exe, $config) {
        if (-not (Test-Path -LiteralPath $path)) {
            throw "Not found: $path (build with: go build -o bin\hecate.exe ./cmd/hecate)"
        }
    }
    & $exe check -config $config | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "hecate check failed for $config; fix the configuration first."
    }
    if (Get-HecateTask) {
        throw "Task '$TaskName' already exists. Run 'remove' first to install it again."
    }

    $user = "$env:USERDOMAIN\$env:USERNAME"
    $taskAction = New-ScheduledTaskAction -Execute $exe -Argument "run -config `"$config`"" -WorkingDirectory $Root
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $trigger.Delay = $BootDelay
    $trigger.Repetition = (New-ScheduledTaskTrigger -Once -At (Get-Date) -RepetitionInterval $RetryEvery -RepetitionDuration $RetryFor).Repetition
    $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType S4U -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
        -StartWhenAvailable -Hidden -MultipleInstances IgnoreNew `
        -ExecutionTimeLimit ([TimeSpan]::Zero)
    Register-ScheduledTask -TaskName $TaskName -Action $taskAction -Trigger $trigger -Principal $principal `
        -Settings $settings -Description 'hecate: pemblokir iklan berbasis DNS (lihat scripts\autostart.ps1)' | Out-Null
    Write-Output "Task '$TaskName' installed for $user. It starts automatically 30 seconds after boot."
    Write-Output "Mulai sekarang: Start-ScheduledTask -TaskName $TaskName   Cabut: scripts\autostart.ps1 remove"
}

function Remove-HecateTask {
    if (-not (Test-Administrator)) {
        throw 'Run from an elevated PowerShell (Administrator).'
    }
    if (-not (Get-HecateTask)) {
        Write-Output "Task '$TaskName' does not exist; nothing to remove."
        return
    }
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Output "Task '$TaskName' removed. The hecate process started by the task was stopped too."
}

function Show-HecateStatus {
    $task = Get-HecateTask
    if (-not $task) {
        Write-Output "Task '$TaskName': not installed."
    } else {
        $info = Get-ScheduledTaskInfo -TaskName $TaskName
        Write-Output "Tugas '$TaskName': $($task.State); last result 0x$('{0:X}' -f $info.LastTaskResult); last run $($info.LastRunTime)"
    }
    $procs = Get-Process -Name hecate -ErrorAction SilentlyContinue
    if ($procs) {
        $procs | ForEach-Object { Write-Output "Proses hecate: PID $($_.Id), mulai $($_.StartTime), $([math]::Round($_.WorkingSet64 / 1MB, 1)) MB" }
    } else {
        Write-Output 'hecate process: not running.'
    }
}

switch ($Action) {
    'install' { Install-HecateTask }
    'remove'  { Remove-HecateTask }
    'status'  { Show-HecateStatus }
}
