# Token Panosu kurulumu ve onarımı (Windows, PowerShell)
#   Yeni kurulum: & ([scriptblock]::Create((irm https://pano.ornek.com/kur.ps1))) -Kod XXXXX-XXXXX
#   Onarım/güncelleme (kod gerekmez): & ([scriptblock]::Create((irm https://pano.ornek.com/kur.ps1)))
# Bu bilgisayara uygun tek dosyalık programı indirir, özetini doğrular, %USERPROFILE%\.token-panosu
# içine koyar ve eşleştirmeyi yapar. Python ya da başka bir şey kurmaz.
param(
  [Parameter(Mandatory = $false)][string]$Kod = "",
  [string]$Adres = $(if ($env:TOKEN_PANOSU_ADRES) { $env:TOKEN_PANOSU_ADRES } else { "https://pano.ornek.com" })
)
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Mimari = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") { "arm64" } else { "amd64" }
$Ad = "token-panosu-windows-$Mimari.exe"

$Dizin = Join-Path $env:USERPROFILE ".token-panosu"
New-Item -ItemType Directory -Force -Path $Dizin | Out-Null
$Exe = Join-Path $Dizin "token-panosu.exe"
$Gecici = Join-Path $Dizin ".indiriliyor.exe"

Write-Host "Indiriliyor: $Ad"
Invoke-WebRequest -UseBasicParsing -Uri "$Adres/indir/$Ad" -OutFile $Gecici
$Beklenen = ((Invoke-WebRequest -UseBasicParsing -Uri "$Adres/indir/$Ad.sha256.txt").Content | Out-String).Trim()
$Bulunan = (Get-FileHash -Algorithm SHA256 -Path $Gecici).Hash.ToLower()
if (-not $Beklenen -or $Beklenen -ne $Bulunan) {
  Remove-Item -Force $Gecici
  throw "Indirilen dosya dogrulanamadi, kurulum durduruldu."
}
Move-Item -Force $Gecici $Exe

# Program pencere açmadan çalışacak biçimde derlendi; çıktısını görmek için boruya yönlendiriyoruz
if ($Kod -and $Kod.Trim() -ne "") {
  & $Exe kur --url $Adres --kod $Kod 2>&1 | Out-Host
} else {
  & $Exe onar --url $Adres 2>&1 | Out-Host
}
exit $LASTEXITCODE
