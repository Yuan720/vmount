param(
    [string]$Config = "vmount.json",
    [string]$Mount = "Z:"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path "vmount.exe")) { throw "vmount.exe not found, run from build dir" }
if (-not (Test-Path $Config)) { throw "config file $Config not found" }

Write-Host "starting vmount..."
$proc = Start-Process -FilePath ".\vmount.exe" -ArgumentList "-config", $Config -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 5
if ($proc.HasExited) { throw "vmount exited early with code $($proc.ExitCode)" }

try {
    $testFile = "$Mount\vmount-test.txt"
    $content = "hello from vmount $([DateTime]::Now.ToString('yyyy-MM-dd HH:mm:ss'))"

    Write-Host "writing $testFile"
    Set-Content -Path $testFile -Value $content
    $readBack = Get-Content -Path $testFile -Raw
    if ($readBack.Trim() -ne $content.Trim()) { throw "readback mismatch: '$readBack'" }
    Write-Host "readback OK"

    Write-Host "listing root"
    Get-ChildItem $Mount | Select-Object Name, Length

    Write-Host "removing $testFile"
    Remove-Item $testFile

    $bigFile = "$Mount\big.bin"
    Write-Host "writing 64MB test file (multipart path)"
    $fs = [System.IO.File]::Create($bigFile)
    $buf = New-Object byte[] (1MB)
    (New-Object Random).NextBytes($buf)
    for ($i = 0; $i -lt 64; $i++) { $fs.Write($buf, 0, $buf.Length) }
    $fs.Close()
    $info = Get-Item $bigFile
    if ($info.Length -ne 67108864) { throw "big file size mismatch: $($info.Length)" }
    Write-Host "big file OK ($($info.Length) bytes)"
    Remove-Item $bigFile

    Write-Host "ALL TESTS PASSED"
}
finally {
    Write-Host "unmounting..."
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
}
