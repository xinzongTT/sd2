# Upload built Linux binary + config to server and run remote-install.sh
# Usage:
#   $env:SSHPASS='your-root-password'
#   .\deploy\upload-and-install.ps1 -Host 45.149.92.155

param(
  [Parameter(Mandatory = $true)][string]$Host,
  [string]$User = "root",
  [string]$RemoteDir = "/opt/higgsfield-proxy"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

if (-not (Test-Path ".\higgsfield-proxy-linux")) {
  Write-Host "Building linux binary..."
  $env:GOOS = "linux"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
  go build -o higgsfield-proxy-linux ./cmd/server
  Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

if (-not $env:SSHPASS) {
  throw "Set env SSHPASS to root password first"
}

$sshBase = @("-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=NUL", "-o", "PreferredAuthentications=password", "-o", "PubkeyAuthentication=no")

function Invoke-SSH([string]$cmd) {
  & sshpass -e ssh @sshBase "${User}@${Host}" $cmd
  if ($LASTEXITCODE -ne 0) { throw "ssh failed: $cmd" }
}

Write-Host "Testing SSH..."
Invoke-SSH "uname -a && mkdir -p $RemoteDir"

Write-Host "Uploading files..."
& sshpass -e scp @sshBase `
  ".\higgsfield-proxy-linux" `
  ".\config.server.yaml" `
  ".\config.example.yaml" `
  ".\deploy\remote-install.sh" `
  "${User}@${Host}:${RemoteDir}/"
if ($LASTEXITCODE -ne 0) { throw "scp failed" }

# optional: upload local accounts if present
if (Test-Path ".\data\accounts") {
  Write-Host "Uploading accounts..."
  & sshpass -e ssh @sshBase "${User}@${Host}" "mkdir -p $RemoteDir/data"
  & sshpass -e scp -r @sshBase ".\data\accounts" ".\data\cli-homes" "${User}@${Host}:${RemoteDir}/data/"
}

Write-Host "Running remote install..."
Invoke-SSH "cd $RemoteDir && mv -f higgsfield-proxy-linux higgsfield-proxy && chmod +x higgsfield-proxy remote-install.sh && bash remote-install.sh"

Write-Host "Done. Open http://${Host}:8317/"
