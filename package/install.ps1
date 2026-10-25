# Installs station-xiaozhi on a rooted Yandex Station over network ADB.
#   .\install.ps1 [-Station 192.168.0.125]                 install or update
#   .\install.ps1 [-Station 192.168.0.125] -Action uninstall
#   .\install.ps1 [-Station 192.168.0.125] -Action status|start|stop|bind
param(
    [string]$Station = "",
    [ValidateSet("install", "uninstall", "status", "start", "stop", "bind")]
    [string]$Action = "install"
)
# adb reports progress on stderr; failures are checked through $LASTEXITCODE instead.
$ErrorActionPreference = "Continue"
# The Station prints UTF-8; Windows PowerShell 5.1 decodes native output with the OEM code page otherwise.
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
Set-Location -LiteralPath $PSScriptRoot
$remote = "/data/local/tmp/station-xiaozhi-setup"

function Fail([string]$message) {
    Write-Host "Ошибка: $message" -ForegroundColor Red
    exit 1
}

# adb: $env:ADB, then a platform-tools folder next to this script, then PATH.
$adb = $env:ADB
if (-not $adb) {
    $local = Join-Path $PSScriptRoot "platform-tools\adb.exe"
    if (Test-Path -LiteralPath $local) { $adb = $local }
    elseif (Get-Command adb -ErrorAction SilentlyContinue) { $adb = (Get-Command adb).Source }
    else { Fail "не найден adb. Скачайте Android platform-tools (https://developer.android.com/tools/releases/platform-tools) и распакуйте папку platform-tools рядом с install.ps1" }
}

if ($Station) {
    if ($Station -notmatch ":") { $Station = "${Station}:5555" }
    & $adb connect $Station | Out-Null
    $serial = $Station
} else {
    $serial = (& $adb devices | Select-Object -Skip 1 | Where-Object { $_ -match "\tdevice$" } | Select-Object -First 1) -replace "\t.*", ""
    if (-not $serial) { Fail "станция не найдена. Укажите её IP: .\install.ps1 -Station 192.168.0.125" }
}

$state = (& $adb -s $serial get-state 2>$null)
if ($state -ne "device") { Fail "нет связи со станцией $serial по ADB" }
if (((& $adb -s $serial shell id -u) -join "").Trim() -ne "0") { Fail "на станции нет root-доступа через ADB" }
Write-Host "Станция: $serial"

& $adb -s $serial shell "rm -rf $remote && mkdir -p $remote" | Out-Null
$files = @("station\setup.sh")
if ($Action -eq "install") { $files = @("station-xiaozhi", "station\run.sh", "station\S99station-xiaozhi", "station\setup.sh") }
foreach ($file in $files) {
    if (-not (Test-Path -LiteralPath $file)) { Fail "в папке установщика нет файла $file" }
    & $adb -s $serial push $file "$remote/" 2>$null | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "не удалось передать $file на станцию" }
}

& $adb -s $serial shell "sh $remote/setup.sh $Action"
$status = $LASTEXITCODE
if ($Action -eq "install" -and $status -eq 0) {
    Write-Host ""
    Write-Host "Проверяю привязку к xiaozhi.me..."
    & $adb -s $serial shell "sh $remote/setup.sh bind"
    $status = $LASTEXITCODE
}
& $adb -s $serial shell "rm -rf $remote" | Out-Null
if ($status -ne 0) { exit $status }
if ($Action -eq "install") {
    Write-Host ""
    Write-Host "Готово. Скажите станции «Яндекс» и задайте вопрос. «Алиса» работает как обычно." -ForegroundColor Green
}
