$ErrorActionPreference = "Continue"
Get-Process vmount -ErrorAction SilentlyContinue | Stop-Process -Force
Start-Sleep -Seconds 1
Remove-Item "C:\Users\Mhtly\Documents\vmount\z3.out.log", "C:\Users\Mhtly\Documents\vmount\z3.err.log" -ErrorAction SilentlyContinue
Start-Process -FilePath "C:\Users\Mhtly\Documents\vmount\vmount.exe" -ArgumentList "-config", "C:\Users\Mhtly\Documents\vmount\vmount.json" -RedirectStandardOutput "C:\Users\Mhtly\Documents\vmount\z3.out.log" -RedirectStandardError "C:\Users\Mhtly\Documents\vmount\z3.err.log" -WindowStyle Hidden
Start-Sleep -Seconds 10
$p = Get-Process vmount -ErrorAction SilentlyContinue
if ($p) { "vmount running: $($p.Id)" } else { "vmount NOT running" }
"--- out ---"
Get-Content "C:\Users\Mhtly\Documents\vmount\z3.out.log" -ErrorAction SilentlyContinue
"--- err ---"
Get-Content "C:\Users\Mhtly\Documents\vmount\z3.err.log" -ErrorAction SilentlyContinue
$z = Get-PSDrive -Name Z -ErrorAction SilentlyContinue
if ($z) { "Z: mounted: $($z.Root)" } else { "Z: not mounted" }
