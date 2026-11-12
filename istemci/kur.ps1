param(
  [string]$Kod = "",
  [string]$Adres = $(if ($env:TOKEN_PANOSU_ADRES) { $env:TOKEN_PANOSU_ADRES } else { "https://pano.ornek.com" })
)
# Token Panosu Windows installer and repair script (PowerShell 5.1+ ASCII-clean)
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Adres = $Adres.TrimEnd("/")
$Mimari = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64") { "arm64" } else { "amd64" }
$TemelAd = "token-panosu-windows-$Mimari.exe"

$Dizin = Join-Path $env:USERPROFILE ".token-panosu"
New-Item -ItemType Directory -Force -Path $Dizin | Out-Null
$AyarDosya = Join-Path $Dizin "ayar.json"
$Exe = Join-Path $Dizin "token-panosu.exe"
$EskiExe = Join-Path $Dizin "token-panosu.exe.eski"
$Gecici = Join-Path $Dizin ".indiriliyor.exe"

$Paket = ""
if ($Kod -and $Kod.Trim() -ne "") {
  try {
    $KodTemiz = [uri]::EscapeDataString($Kod.Trim())
    $Yanit = Invoke-RestMethod -UseBasicParsing -TimeoutSec 5 -Uri "$Adres/api/kurulum-paket?kod=$KodTemiz"
    if ($Yanit -and $Yanit.paket) {
      $Paket = [string]$Yanit.paket
    }
  } catch {}
} elseif (Test-Path -LiteralPath $AyarDosya) {
  try {
    $Ayar = Get-Content -Raw -LiteralPath $AyarDosya -Encoding UTF8 | ConvertFrom-Json
    if ($Ayar -and $Ayar.hedef_paket) {
      $Paket = [string]$Ayar.hedef_paket
    }
  } catch {}
}

$Ad = $TemelAd
if ($Paket -match "^(c|g|m|cg|cm|gm)$") {
  $Ad = "token-panosu-windows-$Mimari-$Paket.exe"
}

Write-Host "Indiriliyor: $Ad"
if ($Ad -ne $TemelAd) {
  try {
    Invoke-WebRequest -UseBasicParsing -Uri "$Adres/indir/$Ad" -OutFile $Gecici
  } catch {
    $HttpKod = 0
    try { $HttpKod = [int]$_.Exception.Response.StatusCode } catch {}
    if ($HttpKod -eq 404) {
      if (Test-Path -LiteralPath $Gecici) { Remove-Item -Force -LiteralPath $Gecici -ErrorAction SilentlyContinue }
      $Ad = $TemelAd
      Write-Host "Indiriliyor: $Ad"
      Invoke-WebRequest -UseBasicParsing -Uri "$Adres/indir/$Ad" -OutFile $Gecici
    } else {
      throw
    }
  }
} else {
  Invoke-WebRequest -UseBasicParsing -Uri "$Adres/indir/$Ad" -OutFile $Gecici
}

$Beklenen = ((Invoke-WebRequest -UseBasicParsing -Uri "$Adres/indir/$Ad.sha256.txt").Content | Out-String).Trim().ToLower()
$Bulunan = (Get-FileHash -Algorithm SHA256 -LiteralPath $Gecici).Hash.ToLower()
if (-not $Beklenen -or $Beklenen -ne $Bulunan) {
  Remove-Item -Force -LiteralPath $Gecici -ErrorAction SilentlyContinue
  throw "Indirilen dosya dogrulanamadi, kurulum durduruldu."
}

if (Test-Path -LiteralPath $EskiExe) {
  Remove-Item -Force -LiteralPath $EskiExe -ErrorAction SilentlyContinue
}
if (Test-Path -LiteralPath $Exe) {
  try { Move-Item -Force -LiteralPath $Exe -Destination $EskiExe -ErrorAction Stop } catch {}
}
Move-Item -Force -LiteralPath $Gecici -Destination $Exe
if (Test-Path -LiteralPath $EskiExe) {
  Remove-Item -Force -LiteralPath $EskiExe -ErrorAction SilentlyContinue
}

if ($Kod -and $Kod.Trim() -ne "") {
  & $Exe kur --url $Adres --kod $Kod.Trim() 2>&1 | Out-Host
} else {
  & $Exe onar --url $Adres 2>&1 | Out-Host
}
exit $LASTEXITCODE
