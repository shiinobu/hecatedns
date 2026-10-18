<#
.SYNOPSIS
  Memasang, menghapus, atau memeriksa tugas Task Scheduler yang menjalankan xdns saat PC menyala.

.DESCRIPTION
  install  Membuat tugas "xdns": jalan 30 detik setelah boot (tanpa perlu login), sebagai akun
           sekarang dengan hak biasa (bukan SYSTEM, bukan "highest"), tersembunyi, tanpa batas
           waktu. Pemicunya diulang tiap 1 menit selama 365 hari sejak tiap boot; karena tugas tidak membuat
           salinan kedua, ulangan itu hanya menghidupkan xdns bila belum jalan (misalnya
           Wi-Fi belum memberi alamat 192.168.1.99 saat boot lalu xdns berhenti dengan galat).
           Perintah ini mengubah Windows; jalankan dari PowerShell Administrator.
  remove   Menghentikan dan menghapus tugas. Ini satu-satunya rollback yang diperlukan.
  status   Menampilkan keadaan tugas dan proses xdns.

  Tugas menjalankan bin\xdns.exe run -config xdns.yaml dari folder proyek. Aturan firewall
  terikat ke jalur bin\xdns.exe, jadi bangun ulang ke jalur yang sama dan jangan pindahkan.
  Log operasional ada di <log.dir>\xdns.log (maks 5 MB, satu cadangan .1).

.EXAMPLE
  powershell -File scripts\autostart.ps1 install
  powershell -File scripts\autostart.ps1 status
  powershell -File scripts\autostart.ps1 remove
#>
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('install', 'remove', 'status')]
    [string]$Action,

    [string]$Root = ''
)

$ErrorActionPreference = 'Stop'
if (-not $Root) {
    $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
    $Root = Split-Path -Parent $scriptDir
}
$Root = (Resolve-Path -LiteralPath $Root).Path
$TaskName = 'xdns'
$RetryEvery = New-TimeSpan -Minutes 1
$RetryFor = New-TimeSpan -Days 365
$BootDelay = 'PT30S'

function Test-Administrator {
    $principal = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-XdnsTask {
    return Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
}

function Install-XdnsTask {
    if (-not (Test-Administrator)) {
        throw 'Jalankan dari PowerShell Administrator: membuat tugas terjadwal mengubah Windows.'
    }
    $exe = Join-Path $Root 'bin\xdns.exe'
    $config = Join-Path $Root 'xdns.yaml'
    foreach ($path in $exe, $config) {
        if (-not (Test-Path -LiteralPath $path)) {
            throw "Tidak ada: $path (bangun dengan: go build -o bin\xdns.exe ./cmd/xdns)"
        }
    }
    & $exe check -config $config | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "xdns check gagal untuk $config; perbaiki konfigurasi dulu."
    }
    if (Get-XdnsTask) {
        throw "Tugas '$TaskName' sudah ada. Jalankan 'remove' dulu bila ingin memasangnya ulang."
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
        -Settings $settings -Description 'xdns: pemblokir iklan berbasis DNS (lihat scripts\autostart.ps1)' | Out-Null
    Write-Output "Tugas '$TaskName' dipasang untuk $user. Jalan otomatis 30 detik setelah boot."
    Write-Output "Mulai sekarang: Start-ScheduledTask -TaskName $TaskName   Cabut: scripts\autostart.ps1 remove"
}

function Remove-XdnsTask {
    if (-not (Test-Administrator)) {
        throw 'Jalankan dari PowerShell Administrator.'
    }
    if (-not (Get-XdnsTask)) {
        Write-Output "Tugas '$TaskName' tidak ada; tidak ada yang dihapus."
        return
    }
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Output "Tugas '$TaskName' dihapus. Proses xdns yang dijalankan tugas ikut dihentikan."
}

function Show-XdnsStatus {
    $task = Get-XdnsTask
    if (-not $task) {
        Write-Output "Tugas '$TaskName': tidak terpasang."
    } else {
        $info = Get-ScheduledTaskInfo -TaskName $TaskName
        Write-Output "Tugas '$TaskName': $($task.State); hasil terakhir 0x$('{0:X}' -f $info.LastTaskResult); jalan terakhir $($info.LastRunTime)"
    }
    $procs = Get-Process -Name xdns -ErrorAction SilentlyContinue
    if ($procs) {
        $procs | ForEach-Object { Write-Output "Proses xdns: PID $($_.Id), mulai $($_.StartTime), $([math]::Round($_.WorkingSet64 / 1MB, 1)) MB" }
    } else {
        Write-Output 'Proses xdns: tidak berjalan.'
    }
}

switch ($Action) {
    'install' { Install-XdnsTask }
    'remove'  { Remove-XdnsTask }
    'status'  { Show-XdnsStatus }
}
