# NornicDB Build & Deploy Script (Windows)
# Usage: .\build.ps1 [build|deploy] [amd64-cuda|amd64-cuda-bge|all]

param([string]$Command = "help", [string]$Arch = "")

$Registry = if ($env:REGISTRY) { $env:REGISTRY } else { "timothyswt" }
$Version = if ($env:VERSION) { $env:VERSION } else { "latest" }
$DockerDir = "docker"

# Windows is always amd64
$HostArch = "amd64"

$Images = @{
    "arm64-metal"     = "$Registry/nornicdb-arm64-metal:$Version"
    "arm64-metal-bge" = "$Registry/nornicdb-arm64-metal-bge:$Version"
    "amd64-cuda"      = "$Registry/nornicdb-amd64-cuda:$Version"
    "amd64-cuda-bge"  = "$Registry/nornicdb-amd64-cuda-bge:$Version"
}

$Dockerfiles = @{
    "arm64-metal"     = "$DockerDir/Dockerfile.arm64-metal"
    "arm64-metal-bge" = "$DockerDir/Dockerfile.arm64-metal"
    "amd64-cuda"      = "$DockerDir/Dockerfile.amd64-cuda"
    "amd64-cuda-bge"  = "$DockerDir/Dockerfile.amd64-cuda"
}

$Platforms = @{
    "arm64-metal"     = "linux/arm64"
    "arm64-metal-bge" = "linux/arm64"
    "amd64-cuda"      = "linux/amd64"
    "amd64-cuda-bge"  = "linux/amd64"
}

$BuildArgs = @{
    "arm64-metal-bge" = "--build-arg EMBED_MODEL=true"
    "amd64-cuda-bge"  = "--build-arg EMBED_MODEL=true"
}

function Build-Image($a) {
    if (-not $Images.ContainsKey($a)) { Write-Host "Unknown: $a" -ForegroundColor Red; exit 1 }
    $variant = if ($a -match "-bge$") { "BGE" } else { "BYOM" }
    $args = if ($BuildArgs.ContainsKey($a)) { $BuildArgs[$a] } else { "" }
    Write-Host "Building: $($Images[$a]) [$variant]" -ForegroundColor Blue
    Invoke-Expression "docker build --platform $($Platforms[$a]) $args -t $($Images[$a]) -f $($Dockerfiles[$a]) ."
    if ($LASTEXITCODE -ne 0) { exit 1 }
    Write-Host "✓ Built $($Images[$a])" -ForegroundColor Green
}

function Push-Image($a) {
    if (-not $Images.ContainsKey($a)) { Write-Host "Unknown: $a" -ForegroundColor Red; exit 1 }
    Write-Host "→ Pushing $($Images[$a])" -ForegroundColor Yellow
    docker push $Images[$a]
    if ($LASTEXITCODE -ne 0) { exit 1 }
    Write-Host "✓ Pushed $($Images[$a])" -ForegroundColor Green
}

function Deploy-Image($a) {
    Build-Image $a
    Push-Image $a
}

switch ($Command.ToLower()) {
    "build" {
        if ($Arch -eq "all") {
            Build-Image "amd64-cuda"
            Build-Image "amd64-cuda-bge"
        }
        elseif ($Arch) { Build-Image $Arch }
        else { Write-Host "Usage: .\build.ps1 build <arch|all>" }
    }
    "deploy" {
        if ($Arch -eq "all") {
            Write-Host "Detected architecture: $HostArch" -ForegroundColor Cyan
            Deploy-Image "amd64-cuda"
            Deploy-Image "amd64-cuda-bge"
            Write-Host "✓ All $HostArch images deployed" -ForegroundColor Green
        }
        elseif ($Arch) { Deploy-Image $Arch }
        else { Write-Host "Usage: .\build.ps1 deploy <arch|all>" }
    }
    "images" {
        Write-Host "Host: $HostArch" -ForegroundColor Cyan
        Write-Host ""
        Write-Host "AMD64 CUDA:"
        Write-Host "  $($Images['amd64-cuda']) [BYOM]"
        Write-Host "  $($Images['amd64-cuda-bge']) [BGE]"
    }
    default {
        Write-Host "NornicDB Build Script (Windows: $HostArch)" -ForegroundColor Cyan
        Write-Host ""
        Write-Host "Usage: .\build.ps1 [build|deploy] <arch|all>"
        Write-Host ""
        Write-Host "Architectures:"
        Write-Host "  amd64-cuda       AMD64 base (BYOM)"
        Write-Host "  amd64-cuda-bge   AMD64 with BGE model"
        Write-Host "  all              Both AMD64 variants"
        Write-Host ""
        Write-Host "Examples:"
        Write-Host "  .\build.ps1 build amd64-cuda"
        Write-Host "  .\build.ps1 deploy amd64-cuda-bge"
        Write-Host "  .\build.ps1 deploy all"
        Write-Host ""
        Write-Host "Config: `$env:REGISTRY=name `$env:VERSION=tag"
    }
}
